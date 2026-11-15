// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ReactNode } from 'react'
import { expect, it, vi } from 'vitest'
import { WorkloadView } from './WorkloadView'
import { WorkloadRolloutNotice } from './WorkloadRolloutNotice'
import type { Topology } from '../../types/core'
import { getWorkloadRolloutActivity } from '../../utils/workload-rollout'

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
  const cronJob = { apiVersion: 'batch/v1', kind: 'CronJob', metadata: { name: 'nightly', namespace: 'batch' }, spec: { schedule: '0 * * * *', suspend: false, successfulJobsHistoryLimit: 2, failedJobsHistoryLimit: 1 }, status: { active: [{ kind: 'Job', namespace: 'batch', name: 'nightly-20260421' }], lastScheduleTime: new Date(Date.now() - 4 * 60 * 60_000 - 60_000).toISOString() } }
  const relationships = { scalers: [{ kind: 'HorizontalPodAutoscaler', group: 'autoscaling', namespace: 'batch', name: 'nightly-scale' }], monitors: [{ kind: 'PodMonitor', group: 'monitoring.coreos.com', namespace: 'batch', name: 'nightly-monitor' }] }
  const renderCron = (resource: Parameters<typeof WorkloadView>[0]['resource']) => renderToStaticMarkup(<WorkloadView kind="cronjobs" namespace="batch" name="nightly" onBack={() => {}} resource={resource} relationships={relationships} activeTab="overview" />)
  const active = renderCron(cronJob)
  for (const label of ['运行记录', '运行中的 Job', '最近调度', '最近成功', '保留历史', '受控于', '监控来源']) expect(active).toContain(label)
  expect(active).toContain('4 小时前')
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

it('localizes generated workload readiness while preserving raw phases, strategies, and controller values', () => {
  const pod = { name: 'api-7f9d8', phase: 'Running', ready: true, containers: ['api'], restartCount: 0, healthLevel: 'healthy' as const }
  const render = (kind: string, resource: any) => renderToStaticMarkup(
    <WorkloadView kind={kind} namespace="demo" name="api" onBack={() => {}} resource={resource} workloadPods={[pod]} activeTab="overview" />,
  )
  const deployment = render('deployments', {
    apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo', generation: 2 },
    spec: { replicas: 2, strategy: { type: 'RollingUpdate' }, template: { spec: { containers: [{ name: 'api', image: 'nginx:1' }] } } },
    status: { observedGeneration: 1, replicas: 2, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1, unavailableReplicas: 1 },
  })
  expect(deployment).toContain('1/2 已更新 · 1 可用')
  expect(deployment).toContain('全部就绪')
  expect(deployment).toContain('Running')
  expect(deployment).toContain('RollingUpdate')

  const generatedDeadline = render('deployments', {
    apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo', generation: 2 },
    spec: { replicas: 1 },
    status: { observedGeneration: 2, conditions: [{ type: 'Progressing', status: 'False', reason: 'ProgressDeadlineExceeded' }] },
  })
  expect(generatedDeadline).toContain('发布已超过期限')
  expect(generatedDeadline).not.toContain('Progress deadline exceeded')

  const backendDeadlineMessage = render('deployments', {
    apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo', generation: 2 },
    spec: { replicas: 1 },
    status: { observedGeneration: 2, conditions: [{ type: 'Progressing', status: 'False', reason: 'ProgressDeadlineExceeded', message: 'Progress deadline exceeded' }] },
  })
  expect(backendDeadlineMessage).toContain('Progress deadline exceeded')
  expect(backendDeadlineMessage).not.toContain('发布已超过期限')

  const daemonSet = render('daemonsets', {
    apiVersion: 'apps/v1', kind: 'DaemonSet', metadata: { name: 'api', namespace: 'demo' },
    spec: { updateStrategy: { type: 'RollingUpdate' }, template: { spec: { containers: [{ name: 'api', image: 'nginx:1' }] } } },
    status: { desiredNumberScheduled: 2, currentNumberScheduled: 2, numberReady: 1, updatedNumberScheduled: 1, numberAvailable: 1, numberUnavailable: 1 },
  })
  expect(daemonSet).toContain('1/2 已更新')
  expect(daemonSet).toContain('1/2 个节点就绪')
  expect(daemonSet).toContain('目标节点')
  expect(daemonSet).toContain('DaemonSet')
  expect(daemonSet).toContain('Running')

  const statefulSet = render('statefulsets', {
    apiVersion: 'apps/v1', kind: 'StatefulSet', metadata: { name: 'api', namespace: 'demo' },
    spec: { replicas: 1, updateStrategy: { type: 'RollingUpdate', rollingUpdate: { partition: 0 } }, template: { spec: { containers: [{ name: 'api', image: 'nginx:1' }] } } },
    status: { replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1, currentRevision: 'api-1', updateRevision: 'api-2' },
  })
  expect(statefulSet).toContain('1/1 已更新 · 1 可用')
  expect(statefulSet).toContain('1/1 就绪 · 1 个副本')
  expect(statefulSet).toContain('RollingUpdate')
  expect(statefulSet).toContain('api-1')
  expect(statefulSet).toContain('api-2')
})

it('localizes only generated rollout details and preserves raw collision messages and reasons', () => {
  const render = (kind: string, resource: any) => renderToStaticMarkup(
    <WorkloadView kind={kind} namespace="demo" name="api" onBack={() => {}} resource={resource} activeTab="overview" />,
  )
  const generated = render('deployments', {
    apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo', generation: 1 },
    spec: { replicas: 0 },
    status: { observedGeneration: 1 },
  })
  expect(generated).toContain('未请求副本')
  expect(generated).not.toContain('No replicas requested')

  const rawConditionMessage = render('deployments', {
    apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo', generation: 1 },
    spec: { replicas: 1 },
    status: { observedGeneration: 1, conditions: [{ type: 'Progressing', status: 'False', reason: 'ProgressDeadlineExceeded', message: 'No replicas requested' }] },
  })
  expect(rawConditionMessage).toContain('No replicas requested')
  expect(rawConditionMessage).not.toContain('未请求副本')

  const rawReasonActivity = getWorkloadRolloutActivity({
    apiVersion: 'argoproj.io/v1alpha1', kind: 'Rollout', metadata: { name: 'api', namespace: 'demo' },
    spec: { replicas: 1 },
    status: { conditions: [{ type: 'InvalidSpec', status: 'True', reason: 'No replicas requested' }] },
  }, 'rollout')
  const rawReason = renderToStaticMarkup(<WorkloadRolloutNotice activity={rawReasonActivity} />)
  expect(rawReasonActivity.detailSource).toBe('raw')
  expect(rawReason).toContain('No replicas requested')
  expect(rawReason).not.toContain('未请求副本')

  const rawStatusActivity = getWorkloadRolloutActivity({
    apiVersion: 'argoproj.io/v1alpha1', kind: 'Rollout', metadata: { name: 'api', namespace: 'demo' },
    spec: { replicas: 1 },
    status: { phase: 'degraded', message: '1/1 ready' },
  }, 'rollout')
  const rawStatusMessage = renderToStaticMarkup(<WorkloadRolloutNotice activity={rawStatusActivity} />)
  expect(rawStatusActivity.detailSource).toBe('raw')
  expect(rawStatusMessage).toContain('1/1 ready')
  expect(rawStatusMessage).not.toContain('1/1 就绪')
})

it('keeps external rollout details raw while local synthetic notice details are translated', () => {
  const rawActivity = {
    phase: 'stalled' as const, active: false, manual: false, label: 'Rollout stalled', detail: 'No replicas requested',
    desired: 1, updated: 0, ready: 0, available: 0,
  }
  const rawNotice = renderToStaticMarkup(<WorkloadRolloutNotice activity={rawActivity} />)
  expect(rawNotice).toContain('No replicas requested')
  expect(rawNotice).not.toContain('未请求副本')

  const syntheticNotice = renderToStaticMarkup(<WorkloadRolloutNotice activity={{
    phase: 'idle', active: false, manual: false, label: 'Stable', detail: '', desired: 1, updated: 1, ready: 1, available: 1,
  }} recentImageSave />)
  expect(syntheticNotice).toContain('模板更改已接受，正在等待实时发布状态')
  expect(syntheticNotice).not.toContain('The template change was accepted')
})

it('localizes the related-manifest object selector without changing native kinds', () => {
  const topology: Topology = {
    nodes: [
      { id: 'deployment', kind: 'Deployment', name: 'api', status: 'healthy', data: { namespace: 'demo', apiVersion: 'apps/v1', resourceKind: 'Deployment' } },
      { id: 'service', kind: 'Service', name: 'api-service', status: 'healthy', data: { namespace: 'demo', apiVersion: 'v1', resourceKind: 'Service' } },
    ],
    edges: [{ id: 'exposes', source: 'service', target: 'deployment', type: 'exposes' }],
  }
  const html = renderToStaticMarkup(
    <WorkloadView
      kind="Deployment"
      namespace="demo"
      name="api"
      onBack={() => {}}
      resource={{ apiVersion: 'apps/v1', kind: 'Deployment', metadata: { name: 'api', namespace: 'demo' }, spec: {}, status: {} }}
      topology={topology}
      activeTab="yaml"
      renderRelatedYaml={() => <div>Related manifest</div>}
    />,
  )
  expect(html).toContain('对象')
  expect(html).toContain('api-service')
  expect(html).toContain('Service')
  expect(html).not.toContain('Objects')
})
