import { findAPIResourceForRoute } from '../api/apiResources'
import { workspaceForView } from '../integrations/workspaceRoutes'
import type { APIResource } from '../types'

// Preserve the pick on cluster-wide views, but explain why it does not filter them.
export function namespaceFilterDisabled(
  view: string,
  pathname: string,
  search = '',
  apiResources?: APIResource[],
): { disabled: boolean; tooltip?: string } {
  if (view === 'cost' && !pathname.startsWith('/cost/rightsizing')) {
    return {
      disabled: true,
      tooltip: 'Cost is reported per namespace across the whole cluster — the namespace filter doesn’t apply here.',
    }
  }
  const workspace = workspaceForView(view)
  if (workspace?.namespaceScope === 'cluster') return { disabled: true, tooltip: workspace.namespaceFilterTooltip }
  if (view === 'checks' && pathname.startsWith('/checks/upgrade')) {
    return {
      disabled: true,
      tooltip: 'Upgrade impact scans every namespace you can access — the namespace filter doesn’t apply.',
    }
  }
  const segments = pathname.replace(/^\//, '').split('/')
  if (view === 'gitops' && segments[1] === 'detail') {
    return {
      disabled: true,
      tooltip: 'This resource manages workloads across namespaces — the namespace filter doesn’t apply to its tree.',
    }
  }
  if (view === 'resources') {
    const kindSlug = segments[1]
    const group = new URLSearchParams(search).get('apiGroup') || ''
    const match = kindSlug ? findAPIResourceForRoute(apiResources, kindSlug, group) : undefined
    if (match && !match.namespaced) {
      return {
        disabled: true,
        tooltip: `${match.kind} is a cluster-scoped resource — namespaces don’t apply.`,
      }
    }
  }
  return { disabled: false }
}
