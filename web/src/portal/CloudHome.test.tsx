// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, expect, it, vi } from 'vitest'
import { CloudHome } from './CloudHome'
import { CloudIssues } from './CloudFleetViews'
import type { HubCluster } from './cloudApi'

const responses = new Map<string, unknown>()
const fetching = new Set<string>()
vi.mock('./cloudFleet', async importOriginal => ({
  ...await importOriginal<typeof import('./cloudFleet')>(),
  useFleetQuery: (_clusters: unknown, path: string) => ({ data: responses.get(path), isPending: false, isFetching: fetching.has(path) }),
}))

const cluster = { id: 'demo', name: 'Demo', state: 'connected' } as HubCluster
const result = (data: unknown) => [{ cluster, data }]
const home = () => renderToStaticMarkup(<CloudHome clusters={[cluster]} search={null} />)

beforeEach(() => {
  responses.clear()
  fetching.clear()
  responses.set('/dashboard', result({ cluster: { connected: true } }))
  responses.set('/issues', result({ issues: [], total: 0, total_matched: 0 }))
})

it('does not treat legacy empty issues as healthy when namespace access is restricted', () => {
  responses.set('/dashboard', result({ cluster: { connected: false }, accessRestricted: true }))
  const html = home()
  expect(html).toContain('Namespace access is unavailable in Demo')
  expect(html).toContain('Health is not fully known')
  expect(html).not.toContain('Fleet is healthy')
  expect(html).not.toContain('is-healthy')
})

it.each(['degraded', 'limited', 'unknown'])('does not claim all-clear for %s issue visibility on either fleet surface', state => {
  responses.set('/issues', result({ issues: [], visibility: { state } }))
  expect(home()).not.toContain('Fleet is healthy')
  expect(renderToStaticMarkup(<CloudIssues clusters={[cluster]} />)).toContain('Issue visibility is incomplete')
})

it.each([
  { cluster: { connected: false } },
  { cluster: { connected: true }, partialData: ['pods'] },
  { cluster: { connected: true }, deferredLoading: true },
])('requires complete dashboard coverage before declaring health (%j)', dashboard => {
  responses.set('/dashboard', result(dashboard))
  expect(home()).toContain('Health is not fully known')
})

it('does not declare health while refreshing or missing a source', () => {
  fetching.add('/dashboard')
  expect(home()).not.toContain('Fleet is healthy')
  fetching.clear()
  responses.set('/dashboard', [{ cluster, error: new Error('Agent disconnected') }])
  expect(home()).toContain('Health is not fully known')
})

it('allows an all-clear only when both sources report complete visible scope', () => {
  expect(home()).toContain('Fleet is healthy')
  expect(home()).toContain('No live issues in your visible fleet scope right now.')
})
