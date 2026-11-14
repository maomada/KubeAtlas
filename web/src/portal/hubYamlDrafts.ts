import { clearYamlDraftNamespace, reconcileHubYamlDraftScope } from '@maomada/k8s-ui'

const HUB_YAML_DRAFT_CHANNEL = 'kubeatlas-hub-yaml-draft-purge'
export type HubYamlDraftInvalidationEvent =
  | { type: 'purge'; source: 'local' | 'remote' }
  | { type: 'purge-cleared'; source: 'remote' }
  | { type: 'purge-failed'; source: 'remote'; error: Error }
  | { type: 'logout-failed'; error: unknown }
type HubYamlDraftInvalidationListener = (event: HubYamlDraftInvalidationEvent) => void
const invalidationListeners = new Map<HubYamlDraftInvalidationListener, number>()
let subscriberCount = 0
let broadcastChannel: BroadcastChannel | undefined

function notifyInvalidation(event: HubYamlDraftInvalidationEvent) {
  for (const listener of [...invalidationListeners.keys()]) listener(event)
}

function handleBroadcastMessage(event: MessageEvent<unknown>) {
  if (!event.data || typeof event.data !== 'object' || (event.data as { type?: unknown }).type !== 'purge') return
  notifyInvalidation({ type: 'purge', source: 'remote' })
  const cleared = clearHubYamlDraftsLocally()
  notifyInvalidation(cleared
    ? { type: 'purge-cleared', source: 'remote' }
    : {
      type: 'purge-failed',
      source: 'remote',
      error: new Error('Unable to remove saved YAML drafts after a session change in another tab. This tab remains locked.'),
    })
}

function openBroadcastChannel() {
  if (broadcastChannel || typeof BroadcastChannel === 'undefined') return broadcastChannel
  let channel: BroadcastChannel | undefined
  try {
    channel = new BroadcastChannel(HUB_YAML_DRAFT_CHANNEL)
    channel.addEventListener('message', handleBroadcastMessage)
    broadcastChannel = channel
    return channel
  } catch {
    channel?.close()
    return undefined
  }
}

function closeBroadcastChannel() {
  const channel = broadcastChannel
  if (!channel) return
  broadcastChannel = undefined
  try {
    channel.removeEventListener('message', handleBroadcastMessage)
  } finally {
    channel.close()
  }
}

export function hubYamlDraftScope(authority: string, clusterId: string, apiBase: string): string {
  return JSON.stringify({ authority, clusterId, apiBase })
}

export function reconcileHubYamlDrafts(authority: string, clusterScope?: string): boolean {
  return reconcileHubYamlDraftScope(authority, clusterScope)
}

export function clearHubYamlDraftsLocally(): boolean {
  return clearYamlDraftNamespace()
}

export function invalidateHubYamlDrafts(): boolean {
  notifyInvalidation({ type: 'purge', source: 'local' })
  const cleared = clearHubYamlDraftsLocally()
  const closeAfterPublish = subscriberCount === 0
  const channel = openBroadcastChannel()
  try {
    channel?.postMessage({ type: 'purge' })
  } catch {
    return cleared
  } finally {
    if (closeAfterPublish) closeBroadcastChannel()
  }
  return cleared
}

export function reportHubYamlLogoutFailure(error: unknown) {
  notifyInvalidation({ type: 'logout-failed', error })
}

export function subscribeToHubYamlDraftInvalidation(onInvalidation: HubYamlDraftInvalidationListener): () => void {
  invalidationListeners.set(onInvalidation, (invalidationListeners.get(onInvalidation) ?? 0) + 1)
  subscriberCount++
  openBroadcastChannel()
  let subscribed = true
  return () => {
    if (!subscribed) return
    subscribed = false
    const references = invalidationListeners.get(onInvalidation) ?? 0
    if (references <= 1) invalidationListeners.delete(onInvalidation)
    else invalidationListeners.set(onInvalidation, references - 1)
    subscriberCount--
    if (subscriberCount === 0) closeBroadcastChannel()
  }
}
