// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import type { TimelineEvent } from '../../types'
import { TimelineList } from './TimelineList'

const NOW = Date.now()
const crash = (id: string, secondsAgo: number, over: Partial<TimelineEvent> = {}): TimelineEvent => ({
  id,
  timestamp: new Date(NOW - secondsAgo * 1000).toISOString(),
  source: 'informer',
  kind: 'Pod',
  namespace: 'default',
  name: 'api-1',
  eventType: 'update',
  healthState: 'unhealthy',
  ...over,
})
const ROWS = [crash('newest', 10), crash('older-1', 20), crash('older-2', 30)]
const foldAll = () => true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  container = document.createElement('div')
  root = createRoot(container)
})
afterEach(() => {
  act(() => root.unmount())
})

const render = (events: TimelineEvent[]) =>
  act(async () => root.render(<TimelineList events={events} isLoading={false} foldPerResource={foldAll} />))
const foldButton = () => [...container.querySelectorAll('button')].find((b) => /另有 .* Pod 事件/.test(b.textContent ?? ''))!
const isOpen = () => foldButton().getAttribute('aria-expanded') === 'true'
const click = () => act(async () => foldButton().click())

it('starts closed and toggles on click', async () => {
  await render(ROWS)
  expect(container.textContent).toContain('（1 项）')
  expect(isOpen()).toBe(false)
  await click()
  expect(isOpen()).toBe(true)
  await click()
  expect(isOpen()).toBe(false)
})

it('stays open as newer rows for the same resource arrive', async () => {
  await render(ROWS)
  await click()
  await render([crash('even-newer', 1), ...ROWS])
  expect(isOpen()).toBe(true)
  expect(foldButton().textContent).toContain('另有 3 条 Pod 事件')
})

it('localizes generated age while preserving a colliding raw message and reason', async () => {
  const createdAt = new Date(Date.now() - 18 * 60 * 60 * 1000).toISOString()
  await act(async () => root.render(
    <TimelineList
      events={[crash('raw-copy', 18 * 60 * 60, {
        source: 'k8s_event',
        eventType: 'Warning',
        reason: 'First occurrence',
        message: '18h old',
        createdAt,
      })]}
      isLoading={false}
    />,
  ))

  expect(container.textContent).toContain('18小时前')
  expect(container.textContent).toContain('First occurrence')
  expect(container.textContent).toContain('18h old')
})

it('localizes change and occurrence controls without rewriting occurrence messages', async () => {
  await act(async () => root.render(
    <TimelineList
      events={[crash('changed', 10, {
        diff: { summary: '', fields: [{ path: 'spec.replicas', oldValue: 1, newValue: 2 }] },
      })]}
      isLoading={false}
    />,
  ))
  const expandChanges = container.querySelector<HTMLButtonElement>('button[aria-label="展开变更"]')!
  expect(expandChanges).toBeTruthy()
  act(() => expandChanges.click())
  expect(container.querySelector('button[aria-label="收起变更"]')).toBeTruthy()
  expect(container.textContent).toContain('变更：')

  const timestamped = (id: string, secondsAgo: number, message: string) =>
    crash(id, secondsAgo, {
      source: 'k8s_event',
      eventType: 'Warning',
      reason: 'First occurrence',
      message,
    })
  await act(async () => root.render(
    <TimelineList
      events={[timestamped('first', 18 * 60 * 60, 'First occurrence'), timestamped('last', 17 * 60 * 60, '18h ago')]}
      isLoading={false}
    />,
  ))
  const expandOccurrences = container.querySelector<HTMLButtonElement>('button[aria-label="展开发生记录"]')!
  expect(expandOccurrences).toBeTruthy()
  act(() => expandOccurrences.click())
  expect(container.querySelector('button[aria-label="收起发生记录"]')).toBeTruthy()
  expect(container.textContent).toContain('首次出现')
  expect(container.textContent).toContain('First occurrence')
  expect(container.textContent).toContain('18h ago')
})
