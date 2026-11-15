// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ReactNode } from 'react'
import { expect, it, vi } from 'vitest'
import { WorkloadView } from './WorkloadView'

const renderer = vi.hoisted(() => vi.fn(({ mainFooter }: { mainFooter?: ReactNode }) => <div>Generic renderer and Conditions{mainFooter}</div>))
vi.mock('../shared/ResourceRendererDispatch', async (original) => ({ ...await original<typeof import('../shared/ResourceRendererDispatch')>(), ResourceRendererDispatch: renderer }))

const resource = { apiVersion: 'postgresql.cnpg.io/v1', kind: 'Cluster', metadata: { name: 'pg', namespace: 'db', labels: { app: 'postgres' } }, spec: { instances: 2 }, status: {} }
const props = { kind: 'clusters', namespace: 'db', name: 'pg', group: 'postgresql.cnpg.io', resource, onBack: () => {}, activeTab: 'spec' as const, renderSummary: () => <div>Composed Overview</div>, renderOverviewExtra: () => <div>Audit Findings</div> }

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('replaces the entire expanded spec body, including its generic sections and lead', () => {
  renderer.mockClear()
  const html = renderToStaticMarkup(<WorkloadView {...props} specTab={{ label: 'Configuration', lead: <div>Old lead</div>, render: () => <div>Purpose-built configuration</div> }} />)
  expect(html).toContain('Purpose-built configuration')
  expect(html).toContain('Configuration')
  for (const text of ['Generic renderer', 'Conditions', 'Audit Findings', 'Labels (1)', 'Metadata', 'Recent Events', 'Old lead']) expect(html).not.toContain(text)
  expect(renderer).not.toHaveBeenCalled()
})

it('preserves the generic spec body and lead when no replacement is supplied', () => {
  const html = renderToStaticMarkup(<WorkloadView {...props} specTab={{ label: 'Settings', lead: <div>Existing lead</div> }} />)
  for (const text of ['Settings', 'Existing lead', 'Generic renderer and Conditions', 'Audit Findings']) expect(html).toContain(text)
})

it('does not use the spec replacement in a drawer or on Overview', () => {
  const replacement = vi.fn(() => <div>Purpose-built configuration</div>)
  let html = renderToStaticMarkup(<WorkloadView {...props} expanded={false} specTab={{ render: replacement }} />)
  expect(html).toContain('Composed Overview')
  html = renderToStaticMarkup(<WorkloadView {...props} activeTab="overview" specTab={{ render: replacement }} />)
  expect(html).toContain('Composed Overview')
  expect(replacement).not.toHaveBeenCalled()
})

it('still opens the original Spec & status in a drawer when a page replacement is provided', () => {
  const replacement = vi.fn(() => <div>Purpose-built configuration</div>)
  const host = document.createElement('div')
  const root = createRoot(host)
  act(() => root.render(<WorkloadView {...props} expanded={false} specTab={{ label: 'Configuration', render: replacement }} />))
  const tab = [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find((b) => b.textContent === '规格与状态')!
  act(() => tab.click())
  expect(tab.getAttribute('aria-selected')).toBe('true')
  expect(host.textContent).toContain('Generic renderer and Conditions')
  expect(host.textContent).toContain('Audit Findings')
  expect(replacement).not.toHaveBeenCalled()
  act(() => root.unmount())
})

it('lets a host render the reported phase while preserving the default derived badge', () => {
  const instance = { ...resource, status: { phase: 'Cluster in healthy state', readyInstances: 1 } }
  for (const expanded of [true, false]) {
    const html = renderToStaticMarkup(<WorkloadView {...props} expanded={expanded} resource={instance} renderStatusBadge={(r) => <span>Phase: {r.status.phase}</span>} />)
    expect(html).toContain('Phase: Cluster in healthy state')
    expect(html).not.toMatch(/badge[^>]*>Degraded/)
    expect(renderToStaticMarkup(<WorkloadView {...props} expanded={expanded} resource={instance} />)).toMatch(/badge[^>]*>Degraded/)
  }
})

it('waits for the resource before asking a host for its status badge', () => {
  const badge = vi.fn((r: any) => <span>Phase: {r.status.phase}</span>)
  for (const expanded of [true, false]) {
    expect(() => renderToStaticMarkup(<WorkloadView {...props} expanded={expanded} resource={undefined} renderStatusBadge={badge} />)).not.toThrow()
  }
  expect(badge).not.toHaveBeenCalled()
})

it.each(['ScheduledBackup', 'Database', 'Publication', 'ClusterImageCatalog', 'Deployment', 'Pod', 'Pooler'])('keeps %s drawer utilities apart from identity and object actions', (kind) => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const name = 'a-long-object-name-that-needs-the-full-drawer-width'
  act(() => root.render(<WorkloadView {...props} name={name} resource={{ ...resource, kind }} expanded={false} onExpand={() => {}} onClose={() => {}} renderStatusBadge={() => <span>Reported status</span>} renderHeaderActions={() => <button>Object action</button>} />))
  const expand = host.querySelector('[aria-label="打开完整视图"]')!
  const utilityRow = expand.parentElement!.parentElement!
  expect(utilityRow.classList.contains('shrink-0')).toBe(true)
  expect(utilityRow.classList.contains('flex-nowrap')).toBe(true)
  expect(utilityRow.querySelector('[aria-label="刷新"]')).not.toBeNull()
  expect(utilityRow.querySelector('[aria-label="关闭"]')).not.toBeNull()
  expect(utilityRow.textContent).not.toContain('Object action')
  // The name has its own line under the badges and controls, so a long one gets the drawer's full width.
  const title = host.querySelector('h2')!
  expect(title.textContent).toBe(name)
  const topRow = utilityRow.parentElement!
  expect(topRow.contains(title)).toBe(false)
  expect(topRow.textContent).toContain('Reported status')
  const action = [...host.querySelectorAll('button')].find((b) => b.textContent === 'Object action')!
  expect(topRow.contains(action)).toBe(false)
  expect(title.parentElement!.contains(action)).toBe(false)
  act(() => root.unmount())
})

it('preserves Diagnose before the resource loads while deferring resource-dependent actions', () => {
  const header = vi.fn(() => <button>Resource action</button>)
  const html = renderToStaticMarkup(<WorkloadView {...props} resource={undefined} expanded={false} renderHeaderActions={header} actionsBarProps={{ renderDiagnose: () => <button>Diagnose this object</button> }} />)
  expect(html).toContain('Diagnose this object')
  expect(header).not.toHaveBeenCalled()
})

it.each(['Deployment', 'Pod', 'ScheduledBackup', 'Database', 'Pooler'])('groups Diagnose with %s drawer utilities and omits empty object actions', (kind) => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const header = vi.fn(() => null)
  const group = kind === 'Deployment' ? 'apps' : kind === 'Pod' ? '' : 'postgresql.cnpg.io'
  const data = { ...resource, kind, apiVersion: group ? `${group}/v1` : 'v1' }
  act(() => root.render(<WorkloadView {...props} kind={`${kind.toLowerCase()}s`} group={group} resource={data} expanded={false} onExpand={() => {}} onClose={() => {}} renderHeaderActions={header} actionsBarProps={{ renderDiagnose: () => <button>Diagnose</button> }} />))
  const utility = host.querySelector('[aria-label="打开完整视图"]')!.parentElement!.parentElement!
  expect(utility.textContent).toContain('Diagnose')
  expect(host.querySelector('[data-action-group=object]')!.matches(':empty')).toBe(true)
  expect(header).toHaveBeenCalledTimes(1)
  act(() => root.render(<WorkloadView {...props} kind={`${kind.toLowerCase()}s`} group={group} resource={data} expanded renderHeaderActions={header} actionsBarProps={{ renderDiagnose: () => <button>Diagnose</button> }} />))
  const refresh = host.querySelector('[aria-label="刷新"]')!
  expect(refresh.parentElement!.parentElement!.textContent).toContain('Diagnose')
  act(() => root.unmount())
})

it('hides the object action band when a host action component has no content yet', () => {
  const EmptyAction = () => null
  const host = document.createElement('div'); const root = createRoot(host)
  act(() => root.render(<WorkloadView {...props} expanded={false} renderHeaderActions={() => <EmptyAction />} />))
  const row = host.querySelector('[data-action-group=object]')!
  expect(row.matches(':empty')).toBe(true)
  expect(row.classList.contains('empty:hidden')).toBe(true)
  act(() => root.render(<WorkloadView {...props} expanded={false} renderHeaderActions={() => <button>Run now</button>} />))
  expect(row.matches(':empty')).toBe(false)
  act(() => root.unmount())
})

it.each(['Deployment', 'Pod', 'Pooler', 'ScheduledBackup'])('groups %s drawer actions with utilities in one toolbar', (kind) => {
  const host = document.createElement('div'); const root = createRoot(host)
  const apiKind = kind === 'Deployment' ? 'deployments' : kind === 'Pod' ? 'pods' : kind === 'Pooler' ? 'poolers' : 'scheduledbackups'
  const group = kind === 'Deployment' ? 'apps' : kind === 'Pod' ? '' : 'postgresql.cnpg.io'
  const data = { ...resource, kind, apiVersion: group ? `${group}/v1` : 'v1', spec: { containers: [{ name: 'postgres', image: 'postgres' }] }, status: { phase: 'Running' } }
  act(() => root.render(<WorkloadView {...props} kind={apiKind} group={group} expanded={false} resource={data} actionsBarProps={{ canExec: true, onOpenTerminal: () => {}, onRestart: () => {} }} renderHeaderActions={() => kind === 'ScheduledBackup' ? <><div><button>Suspend</button><button>Run now</button><button>Edit schedule</button></div><div>Run blocked: no backup destination</div></> : <button>{kind === 'Pooler' ? 'Pause' : 'Object action'}</button>} />))
  const object = host.querySelector('[data-action-group=object]')!
  const utility = host.querySelector('[data-action-group=utility]')!
  expect(object.parentElement).toBe(utility.parentElement)
  expect(object.parentElement!.classList.contains('flex-wrap')).toBe(true)
  expect(utility.classList.contains('ml-auto')).toBe(true)
  expect(object.querySelector('button')).not.toBeNull()
  expect(utility.textContent).toContain('YAML')
  if (kind === 'Pod') expect(object.textContent).toContain('Terminal')
  if (kind === 'Deployment') expect(object.textContent).toContain('重启')
  if (kind === 'ScheduledBackup') { expect(object.querySelectorAll('button')).toHaveLength(3); expect(object.textContent).toContain('Run blocked: no backup destination') }
  act(() => root.unmount())
})

it('localizes serving exposure labels and address fallback while preserving Kubernetes values', () => {
  const deployment = { apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'web', namespace: 'demo' }, spec: { replicas: 1, selector: { matchLabels: { app: 'web' } }, template: { metadata: { labels: { app: 'web' } }, spec: { containers: [] } } }, status: { replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1 } }
  const serviceRef = { kind: 'Service', namespace: 'demo', name: 'web' }
  const ingressRef = { kind: 'Ingress', namespace: 'demo', name: 'web-entry' }
  const render = (relationships: Parameters<typeof WorkloadView>[0]['relationships'], servingResources: Parameters<typeof WorkloadView>[0]['servingResources']) => renderToStaticMarkup(<WorkloadView kind="deployments" namespace="demo" name="web" onBack={() => {}} resource={deployment} relationships={relationships} servingResources={servingResources} />)
  const internal = render({ services: [serviceRef] }, [{ ref: serviceRef, resource: { kind: 'Service', spec: { type: 'ClusterIP' } } }])
  expect(internal).toContain('内部')
  expect(internal).toContain('ClusterIP')
  const loading = render({ services: [serviceRef] }, [{ ref: serviceRef, loading: true }])
  expect(loading).toContain('加载中')
  expect(loading).not.toContain('Loading')
  const external = render({ services: [serviceRef], ingresses: [ingressRef] }, [
    { ref: serviceRef, resource: { kind: 'Service', spec: { type: 'LoadBalancer' }, status: { loadBalancer: { ingress: [{ ip: '203.0.113.20' }] } } } },
    { ref: ingressRef, resource: { kind: 'Ingress', metadata: { name: 'web-entry' }, spec: { rules: [{ host: 'shop.example.test' }] }, status: {} } },
  ])
  expect(external).toContain('外部')
  expect(external).toContain('LoadBalancer')
  expect(external).toContain('shop.example.test')
  expect(external).toContain('地址待分配')
})

it('localizes CronJob and Job chrome while preserving names, phases, and backend messages', () => {
  const cronJob = { apiVersion: 'batch/v1', kind: 'CronJob', metadata: { name: 'nightly', namespace: 'batch' }, spec: { schedule: '0 * * * *', suspend: false, successfulJobsHistoryLimit: 2, failedJobsHistoryLimit: 1 }, status: { active: [{ kind: 'Job', namespace: 'batch', name: 'nightly-20260421' }], lastScheduleTime: '2026-04-21T00:00:00Z' } }
  const relationships = { scalers: [{ kind: 'HorizontalPodAutoscaler', group: 'autoscaling', namespace: 'batch', name: 'nightly-scale' }], monitors: [{ kind: 'PodMonitor', group: 'monitoring.coreos.com', namespace: 'batch', name: 'nightly-monitor' }] }
  const renderCron = (resource: Parameters<typeof WorkloadView>[0]['resource']) => renderToStaticMarkup(<WorkloadView kind="cronjobs" namespace="batch" name="nightly" onBack={() => {}} resource={resource} relationships={relationships} activeTab="overview" />)
  const active = renderCron(cronJob)
  for (const label of ['运行记录', '运行中的 Job', '最近调度', '最近成功', '保留历史', '受控于', '监控来源']) expect(active).toContain(label)
  for (const value of ['CronJob', 'Job', 'nightly-20260421', '0 * * * *']) expect(active).toContain(value)
  for (const english of ['Runs', 'Active jobs', 'Last schedule', 'Retained history', 'Controlled by', 'Monitored By']) expect(active).not.toContain(english)
  const suspended = renderCron({ ...cronJob, spec: { ...cronJob.spec, suspend: true }, status: {} })
  expect(suspended).toContain('没有正在运行的 Job。此 CronJob 已暂停，恢复后才会安排新的运行。')
  expect(suspended).toContain('从未')
  const job = { apiVersion: 'batch/v1', kind: 'Job', metadata: { name: 'batch-run', namespace: 'batch' }, spec: { completions: 1 }, status: { failed: 1, conditions: [{ type: 'Failed', status: 'True', reason: 'BackoffLimitExceeded', message: 'raw job failure message' }] } }
  const failed = renderToStaticMarkup(<WorkloadView kind="jobs" namespace="batch" name="batch-run" onBack={() => {}} resource={job} workloadPods={[]} activeTab="overview" />)
  expect(failed).toContain('当前没有为此 Job 保留的 Pod。')
  expect(failed).toContain('Failed')
  expect(failed).toContain('raw job failure message')
})
