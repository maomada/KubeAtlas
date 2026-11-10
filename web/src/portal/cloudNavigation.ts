import { useEffect, useSyncExternalStore } from 'react'
import { withCrossViewParams } from '../utils/navigation'
import { clusterPage } from './cloudApi'

export function cloudViewHref(path: string, native: boolean, clusterId: string | undefined, currentSearch: string) {
  if (!clusterId) return path
  if (native) return withCrossViewParams(clusterPage(clusterId, path), currentSearch)
  return `${path}?${new URLSearchParams({ cluster: clusterId })}`
}

const locationEvent = 'kubeatlas:portal-location'
const currentLocation = () => window.location.pathname + window.location.search + window.location.hash
const subscribe = (listener: () => void) => {
  window.addEventListener('popstate', listener)
  window.addEventListener(locationEvent, listener)
  return () => {
    window.removeEventListener('popstate', listener)
    window.removeEventListener(locationEvent, listener)
  }
}

export function navigateCloud(href: string, replace = false) {
  const target = new URL(href, window.location.origin)
  if (target.origin !== window.location.origin) throw new Error('Portal navigation must stay on this origin')
  window.history[replace ? 'replaceState' : 'pushState']({}, '', target.pathname + target.search + target.hash)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function useCloudLocation() {
  const location = useSyncExternalStore(subscribe, currentLocation)
  useEffect(() => {
    const push = window.history.pushState
    const replace = window.history.replaceState
    const notify = () => window.dispatchEvent(new Event(locationEvent))
    window.history.pushState = function (...args) { push.apply(this, args); notify() }
    window.history.replaceState = function (...args) { replace.apply(this, args); notify() }
    return () => { window.history.pushState = push; window.history.replaceState = replace }
  }, [])
  return new URL(location, window.location.origin)
}
