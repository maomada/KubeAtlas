// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server'
import { LocaleProvider } from '@maomada/k8s-ui/hooks/useLocale'
import { beforeEach, expect, it, vi } from 'vitest'
import { CloudHome } from './CloudHome'
import { CloudChecks, CloudIssues } from './CloudFleetViews'
import { LocalizedCloudError, type HubCluster } from './cloudApi'

const responses = new Map<string, unknown>()
const fetching = new Set<string>()
vi.mock('./cloudFleet', async importOriginal => ({
  ...await importOriginal<typeof import('./cloudFleet')>(),
  useFleetQuery: (_clusters: unknown, path: string) => ({ data: responses.get(path), isPending: false, isFetching: fetching.has(path) }),
}))

const cluster = { id: 'demo', organizationId: 'org-1', name: 'Demo', state: 'connected' } as HubCluster
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
  expect(html).toContain('无法读取 Demo 的命名空间')
  expect(html).toContain('集群健康状态尚不完整')
  expect(html).not.toContain('集群运行正常')
  expect(html).not.toContain('is-healthy')
})

it.each(['degraded', 'limited', 'unknown'])('does not claim all-clear for %s issue visibility on either fleet surface', state => {
  responses.set('/issues', result({ issues: [], visibility: { state } }))
  expect(home()).not.toContain('集群运行正常')
  expect(renderToStaticMarkup(<CloudIssues clusters={[cluster]} />)).toContain('问题可见性不完整')
})

it.each([
  { cluster: { connected: false } },
  { cluster: { connected: true }, partialData: ['pods'] },
  { cluster: { connected: true }, deferredLoading: true },
])('requires complete dashboard coverage before declaring health (%j)', dashboard => {
  responses.set('/dashboard', result(dashboard))
  expect(home()).toContain('集群健康状态尚不完整')
  expect(home()).toContain('请连接所有已选集群并处理数据覆盖警告')
})

it('does not declare health while refreshing or missing a source', () => {
  fetching.add('/dashboard')
  expect(home()).not.toContain('集群运行正常')
  fetching.clear()
  responses.set('/dashboard', [{ cluster, error: new Error('Agent disconnected') }])
  expect(home()).toContain('集群健康状态尚不完整')
})

it('allows an all-clear only when both sources report complete visible scope', () => {
  expect(home()).toContain('集群运行正常')
  expect(home()).toContain('当前可见集群范围内没有实时问题。')
})

it('localizes grouped-check availability while preserving the cluster name', () => {
  responses.set('/audit', result({ checks: {}, summary: { passing: 0, warning: 0, danger: 0 }, missingInputs: [] }))
  const html = renderToStaticMarkup(<CloudChecks clusters={[cluster]} />)
  expect(html).toContain('Demo：检查分组不可用')
  expect(html).toContain('不可用的输入')
})

it('localizes dashboard copy while preserving native resource names and identifiers', () => {
  const html = home()
  expect(html).toContain('集群')
  expect(html).toContain('Pods、deployments、namespaces、secrets')
  expect(html).toContain('Demo')
  const issues = renderToStaticMarkup(<CloudIssues clusters={[cluster]} />)
  expect(issues).toContain('aria-label="搜索问题"')
  expect(issues).toContain('aria-label="严重程度"')
  expect(issues).toContain('<option value="critical">严重</option>')
  expect(issues).toContain('<option value="warning">警告</option>')
})

it('renders fleet labels, check notices, and local errors in the saved locale', () => {
  responses.set('/dashboard', [{ cluster, error: new LocalizedCloudError('Agent 未连接', 'Agent disconnected') }])
  responses.set('/audit', result({ checks: {}, summary: { passing: 0, warning: 0, danger: 0 }, missingInputs: [] }))
  const key = 'kubeatlas-portal-language'
  const previous = window.localStorage.getItem(key)
  window.localStorage.setItem(key, 'en')
  try {
    const html = renderToStaticMarkup(<LocaleProvider>
      <CloudHome clusters={[cluster]} search={null} />
      <CloudIssues clusters={[cluster]} />
      <CloudChecks clusters={[cluster]} />
    </LocaleProvider>)
    expect(html).toContain('Incomplete cluster coverage')
    expect(html).toContain('Demo: Agent disconnected')
    expect(html).toContain('aria-label="Search issues"')
    expect(html).toContain('Some checks could not run')
    expect(html).toContain('Demo: grouped checks unavailable')
  } finally {
    if (previous === null) window.localStorage.removeItem(key)
    else window.localStorage.setItem(key, previous)
  }
})
