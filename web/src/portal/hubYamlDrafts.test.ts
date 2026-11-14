// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import {
  hubYamlDraftScope,
  invalidateHubYamlDrafts,
  subscribeToHubYamlDraftInvalidation,
  type HubYamlDraftInvalidationEvent,
} from './hubYamlDrafts'

const channelName = 'kubeatlas-hub-yaml-draft-purge'
type TestMessageListener = (event: MessageEvent<unknown>) => void

class TestChannel {
  static instances: TestChannel[] = []
  private listeners = new Set<TestMessageListener>()
  closed = false
  postMessage = vi.fn((data: unknown) => {
    for (const channel of TestChannel.instances) {
      if (channel !== this && channel.name === this.name && !channel.closed) channel.dispatch(data)
    }
  })
  close = vi.fn(() => { this.closed = true })
  addEventListener = vi.fn((_type: string, listener: TestMessageListener) => { this.listeners.add(listener) })
  removeEventListener = vi.fn((_type: string, listener: TestMessageListener) => { this.listeners.delete(listener) })

  constructor(readonly name: string) {
    TestChannel.instances.push(this)
  }

  dispatch(data: unknown) {
    for (const listener of [...this.listeners]) listener({ data } as MessageEvent<unknown>)
  }

  static reset() {
    TestChannel.instances = []
  }
}

beforeEach(() => {
  sessionStorage.clear()
  TestChannel.reset()
})
afterEach(() => vi.unstubAllGlobals())

it('scopes drafts by full authority and logical API target, not physical cluster identity', () => {
  const ownerA = hubYamlDraftScope('full-owner-a-context', 'logical-a', '/c/logical-a/api')
  const viewerB = hubYamlDraftScope('full-viewer-b-context', 'logical-b', '/c/logical-b/api')
  const ownerB = hubYamlDraftScope('full-owner-b-context', 'logical-b', '/c/logical-b/api')
  expect(ownerA).not.toBe(viewerB)
  expect(viewerB).not.toBe(ownerB)
  expect(viewerB).toContain('logical-b')
  expect(viewerB).toContain('/c/logical-b/api')
})

it('shares one channel for local purge publishing without self-delivery', () => {
  vi.stubGlobal('BroadcastChannel', TestChannel)
  sessionStorage.setItem('radar_yaml_draft:ConfigMap/qa/config', 'unsaved')
  sessionStorage.setItem('radar_yaml_draft:hub_authority', 'old-authority')
  sessionStorage.setItem('activeOrganizationId', 'org-a')
  const observedDuringPurge: string[] = []
  const onInvalidation = vi.fn((event: HubYamlDraftInvalidationEvent) => {
    if (event.type === 'purge' && event.source === 'local') {
      observedDuringPurge.push(sessionStorage.getItem('radar_yaml_draft:ConfigMap/qa/config') ?? '')
    }
  })
  const secondListener = vi.fn()
  const cleanup = subscribeToHubYamlDraftInvalidation(onInvalidation)
  const cleanupSecond = subscribeToHubYamlDraftInvalidation(secondListener)
  const channel = TestChannel.instances[0]

  try {
    expect(invalidateHubYamlDrafts()).toBe(true)
    expect(sessionStorage.getItem('radar_yaml_draft:ConfigMap/qa/config')).toBeNull()
    expect(sessionStorage.getItem('radar_yaml_draft:hub_authority')).toBeNull()
    expect(sessionStorage.getItem('activeOrganizationId')).toBe('org-a')
    expect(observedDuringPurge).toEqual(['unsaved'])
    expect(onInvalidation).toHaveBeenCalledTimes(1)
    expect(onInvalidation).toHaveBeenCalledWith({ type: 'purge', source: 'local' })
    expect(secondListener).toHaveBeenCalledTimes(1)
    expect(secondListener).toHaveBeenCalledWith({ type: 'purge', source: 'local' })
    expect(TestChannel.instances).toHaveLength(1)
    expect(channel.name).toBe(channelName)
    expect(channel.postMessage).toHaveBeenCalledExactlyOnceWith({ type: 'purge' })
    cleanup()
    expect(channel.close).not.toHaveBeenCalled()
    cleanupSecond()
    expect(channel.removeEventListener).toHaveBeenCalledOnce()
    expect(channel.close).toHaveBeenCalledOnce()
  } finally {
    cleanup()
    cleanupSecond()
  }
})

it('closes a temporary publisher when invalidation has no subscribers', () => {
  vi.stubGlobal('BroadcastChannel', TestChannel)

  expect(invalidateHubYamlDrafts()).toBe(true)
  expect(TestChannel.instances).toHaveLength(1)
  expect(TestChannel.instances[0].postMessage).toHaveBeenCalledTimes(1)
  expect(TestChannel.instances[0].postMessage).toHaveBeenCalledWith({ type: 'purge' })
  expect(TestChannel.instances[0].removeEventListener).toHaveBeenCalledOnce()
  expect(TestChannel.instances[0].close).toHaveBeenCalledOnce()
})

it('notifies and gates before clearing a remote purge, then reports completion', () => {
  vi.stubGlobal('BroadcastChannel', TestChannel)
  const draftKey = 'radar_yaml_draft:ConfigMap/qa/config'
  sessionStorage.setItem(draftKey, 'unsaved')
  const observed: string[] = []
  const onInvalidation = vi.fn((event: HubYamlDraftInvalidationEvent) => {
    observed.push(`${event.type}:${'source' in event ? event.source : ''}:${sessionStorage.getItem(draftKey) ?? '-'}`)
  })
  const cleanup = subscribeToHubYamlDraftInvalidation(onInvalidation)
  const localChannel = TestChannel.instances[0]
  const remoteSender = new TestChannel(channelName)

  try {
    remoteSender.postMessage({ type: 'purge' })
    expect(observed).toEqual(['purge:remote:unsaved', 'purge-cleared:remote:-'])
    expect(onInvalidation).toHaveBeenCalledTimes(2)
    expect(sessionStorage.getItem(draftKey)).toBeNull()
    cleanup()
    expect(localChannel.removeEventListener).toHaveBeenCalledOnce()
    expect(localChannel.close).toHaveBeenCalledOnce()
  } finally {
    cleanup()
    remoteSender.close()
  }
})

it('reports remote storage failure without claiming that drafts were discarded', () => {
  vi.stubGlobal('BroadcastChannel', TestChannel)
  const draftKey = 'radar_yaml_draft:ConfigMap/qa/config'
  sessionStorage.setItem(draftKey, 'unsaved')
  const observed: Array<{ type: string; source?: string; draft: string | null; message?: string }> = []
  const onInvalidation = vi.fn((event: HubYamlDraftInvalidationEvent) => {
    observed.push({
      type: event.type,
      source: 'source' in event ? event.source : undefined,
      draft: sessionStorage.getItem(draftKey),
      message: 'error' in event && event.error instanceof Error ? event.error.message : undefined,
    })
  })
  const cleanup = subscribeToHubYamlDraftInvalidation(onInvalidation)
  const remoteSender = new TestChannel(channelName)
  const removeItem = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
    throw new Error('Storage access denied')
  })

  try {
    remoteSender.postMessage({ type: 'purge' })
    expect(observed.map(({ type, source, draft }) => [type, source, draft])).toEqual([
      ['purge', 'remote', 'unsaved'],
      ['purge-failed', 'remote', 'unsaved'],
    ])
    expect(observed[1].message).toBe('Unable to remove saved YAML drafts after a session change in another tab. This tab remains locked.')
    expect(observed.some(event => event.type === 'purge-cleared')).toBe(false)
    expect(removeItem).toHaveBeenCalled()
    expect(sessionStorage.getItem(draftKey)).toBe('unsaved')
  } finally {
    removeItem.mockRestore()
    cleanup()
    remoteSender.close()
  }
})
