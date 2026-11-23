// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ApplicationsView } from './ApplicationsView'
import { LocaleProvider } from '../../hooks/useLocale'
import { SearchBox } from '../ui/SearchBox'
import {
  buildSingleAppEntry,
  searchTextForEntry,
  foldAppGroups,
  CLASS_META,
  type AppRow,
  type FleetAppEntry,
  type SingleAppEntry,
  rolloutSummaryForApps,
} from '../../utils/applications'
import { CategoryTooltip, ProvenanceTooltip } from './AppTooltips'
import { ReadyBar } from './ReadyBar'

// Parity tests for the pure core the shared ApplicationsView is built on:
// buildSingleAppEntry (the extracted former buildEntry), the search-text helper,
// and the fold. These pin that the single variant behaves exactly as the old
// ApplicationsList body did — fold grouping, facet counts, search matching.

const wl = (over: Partial<AppRow['workloads'][number]> = {}): AppRow['workloads'][number] => ({
  kind: 'Deployment',
  namespace: 'default',
  name: 'svc',
  workload_class: 'service',
  health: 'healthy',
  ready: 1,
  desired: 1,
  restarts: 0,
  ...over,
})

const app = (over: Partial<AppRow> = {}): AppRow => ({
  key: over.key ?? 'default/svc',
  name: over.name ?? 'svc',
  namespace: 'default',
  health: 'healthy',
  workloads: [wl()],
  ...over,
})

describe('buildSingleAppEntry', () => {
  it('matches Chinese and English labels without changing filter values', () => {
    const entry = buildSingleAppEntry(app({ workload_class: 'service' }))
    const text = searchTextForEntry(entry)

    expect(text).toContain('service')
    expect(text).toContain('服务')
    expect(text).toContain('app')
    expect(text).toContain('应用')
    expect(entry.workloadClass).toBe('service')
    expect(entry.classSet).toEqual(['service'])
    expect(entry.category).toBe('app')
    expect(CLASS_META.unknown.tooltip).toBe('无法根据工作负载推断运行类别。')
  })

  it('renders coherent provenance wording for label grouping', () => {
    const html = renderToStaticMarkup(<ProvenanceTooltip tier={6} appKey="default/api" confidence="high" />)
    expect(html).toContain('按标签')
    expect(html).toContain('app.kubernetes.io/instance')
    expect(html).toContain('分组')
    expect(html).not.toContain('标签</code> 标签')

    const category = renderToStaticMarkup(<CategoryTooltip category="addon" />)
    expect(category).toContain('平台附加组件')
    expect(category).not.toContain('Platform add-on')
  })

  it('keeps native resource kinds and namespace/name identity values in search data', () => {
    const entry = buildSingleAppEntry(app({
      key: 'secure/api-secret',
      name: 'api-secret',
      namespace: 'secure',
      workloads: [wl({ kind: 'Secret', namespace: 'secure', name: 'api-secret' })],
    }))
    expect(entry.kinds).toEqual({ Secret: 1 })
    const text = searchTextForEntry(entry)
    expect(text).toContain('secret')
    expect(text).toContain('secure')
    expect(text).toContain('api-secret')
  })

  it('tags the single variant and aggregates ready/desired + kinds', () => {
    const e = buildSingleAppEntry(
      app({
        workloads: [
          wl({ kind: 'Deployment', ready: 2, desired: 3 }),
          wl({ kind: 'CronJob', workload_class: 'job', ready: 0, desired: 0 }),
        ],
      }),
    )
    expect(e.variant).toBe('single')
    expect(e.ready).toBe(2)
    expect(e.desired).toBe(3)
    expect(e.readyRatio).toBeCloseTo(2 / 3)
    expect(e.kinds).toEqual({ Deployment: 1, CronJob: 1 })
    // service + job → mixed for the facet set; class set is inclusive.
    expect(e.classSet.sort()).toEqual(['job', 'service'])
  })

  it('resolves env from the namespace heuristic and marks it inferred', () => {
    const e = buildSingleAppEntry(app({ namespace: 'billing-prod', workloads: [wl({ namespace: 'billing-prod' })] }))
    expect(e.env).toBe('prod')
    expect(e.envInferred).toBe(true)
  })

  it('takes the identity env as authoritative (not inferred when declared)', () => {
    const e = buildSingleAppEntry(
      app({
        identity: { key: 'svc', env: 'staging', confidence: 'high', evidence: 'declared source path' },
      }),
    )
    expect(e.env).toBe('staging')
    expect(e.envInferred).toBe(false)
  })

  it('uses delivery degradation in the displayed application health', () => {
    const e = buildSingleAppEntry(app({
      health: 'degraded',
      runtimeHealth: 'healthy',
      sourceStatus: { sync: 'Synced', health: 'Degraded' },
    }))

    expect(e.health).toBe('degraded')
  })

  it('preserves the latest retained batch outcome when delivery adds no degradation', () => {
    const e = buildSingleAppEntry(app({
      health: 'unhealthy',
      runtimeHealth: 'unhealthy',
      workload_class: 'job',
      workloads: [wl({
        kind: 'CronJob',
        workload_class: 'job',
        health: 'unhealthy',
        ready: 0,
        desired: 0,
        batch: { retainedRuns: 2, failedRuns: 1, succeededRuns: 1, latestRunPhase: 'Succeeded' },
      })],
    }))

    expect(e.health).toBe('healthy')
  })

  it('applies delivery degradation to a batch app with a successful latest run', () => {
    const e = buildSingleAppEntry(app({
      health: 'unhealthy',
      runtimeHealth: 'unhealthy',
      workload_class: 'job',
      sourceStatus: { sync: 'Synced', health: 'Degraded' },
      workloads: [wl({
        kind: 'CronJob',
        workload_class: 'job',
        health: 'unhealthy',
        ready: 0,
        desired: 0,
        batch: { retainedRuns: 2, failedRuns: 1, succeededRuns: 1, latestRunPhase: 'Succeeded' },
      })],
    }))

    expect(e.health).toBe('degraded')
  })
})

describe('ReadyBar', () => {
  it('caps target readiness during a surge', () => {
    const html = renderToStaticMarkup(<ReadyBar ready={2} desired={1} />)
    expect(html).toContain('1/1')
    expect(html).not.toContain('2/1')
  })
})

describe('rolloutSummaryForApps', () => {
  it('surfaces a healthy surge rollout as activity rather than degradation', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: [wl({
        ready: 3,
        desired: 3,
        rollout: { phase: 'progressing', active: true, manual: false, label: 'Rolling out', detail: '2/3 updated · 3 available', desired: 3, updated: 2, ready: 3, available: 3 },
      })],
    })])

    expect(summary).toMatchObject({ label: 'Rolling out', health: 'neutral', count: 1 })
    expect(summary?.detail).toContain('2/3 updated')
  })

  it('keeps rollout copy while preserving a worse workload health tone', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: [wl({
        health: 'unhealthy',
        rollout: { phase: 'progressing', active: true, manual: false, label: 'Rolling out', desired: 3, updated: 1, ready: 0, available: 0 },
      })],
    })])

    expect(summary).toMatchObject({ label: 'Rolling out', health: 'unhealthy' })
  })

  it('escalates a stalled revision without changing stable app health data', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: [wl({
        rollout: { phase: 'stalled', active: false, manual: false, label: 'New revision cannot start', detail: 'ImagePullBackOff', desired: 1, updated: 0, ready: 1, available: 1 },
      })],
    })])

    expect(summary).toMatchObject({ label: 'New revision cannot start', health: 'unhealthy' })
  })

  it('names stalled work before the other pending changes in a grouped app', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: [
        wl({ name: 'broken', rollout: { phase: 'stalled', active: false, manual: false, label: 'Rollout stalled', desired: 1, updated: 0, ready: 1, available: 1 } }),
        wl({ name: 'manual', rollout: { phase: 'paused', active: true, manual: false, label: 'Rollout paused', desired: 1, updated: 0, ready: 1, available: 1 } }),
      ],
    })])

    expect(summary).toMatchObject({ label: '1 个停滞 · 1 个等待中', health: 'unhealthy', count: 2 })
  })

  it('does not report zero waiting workloads when every change is stalled', () => {
    const stalled = { phase: 'stalled' as const, active: false, manual: false, label: 'Rollout stalled', desired: 1, updated: 0, ready: 1, available: 1 }
    const summary = rolloutSummaryForApps([app({
      workloads: [wl({ name: 'one', rollout: stalled }), wl({ name: 'two', rollout: stalled })],
    })])

    expect(summary?.label).toBe('2 个停滞')
  })

  it('does not promote inactive rollout policies to app-level activity', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: [
        wl({ rollout: { phase: 'paused', active: false, manual: false, label: 'Rollout paused', desired: 1, updated: 1, ready: 0, available: 0 } }),
        wl({ rollout: { phase: 'partition-reached', active: false, manual: false, label: 'Partition reached', desired: 3, updated: 2, ready: 2, available: 2 } }),
      ],
    })])

    expect(summary).toBeNull()
  })

  it('caps grouped rollout details while preserving the full structured list', () => {
    const summary = rolloutSummaryForApps([app({
      workloads: Array.from({ length: 10 }, (_, index) => wl({
        name: `worker-${index}`,
        rollout: { phase: 'progressing', active: true, manual: false, label: 'Rolling out', desired: 1, updated: 0, ready: 1, available: 1 },
      })),
    })])

    expect(summary).toMatchObject({ label: '10 个正在更新', count: 10 })
    expect(summary?.details).toHaveLength(10)
    expect(summary?.detail).toContain('另有 2 项')
  })
})

describe('searchTextForEntry (single)', () => {
  it('matches on name, namespace, version, env, and workload kind', () => {
    const e = buildSingleAppEntry(
      app({
        name: 'checkout',
        namespace: 'shop-prod',
        versions: ['1.4.2'],
        workloads: [wl({ kind: 'StatefulSet', namespace: 'shop-prod' })],
      }),
    )
    const text = searchTextForEntry(e)
    expect(text).toContain('checkout')
    expect(text).toContain('shop-prod')
    expect(text).toContain('1.4.2')
    expect(text).toContain('prod')
    expect(text).toContain('statefulset')
    expect(text).not.toContain('zzz-no-match')
  })
})

// One logical app across two envs must fold into a single group row with two
// instance children; an orphan (filtered to one member) must render flat.
describe('foldAppGroups over single entries', () => {
  const ident = (env: string) => ({ key: 'shop', env, confidence: 'high', evidence: 'declared source path' })

  const mkSibling = (env: string): SingleAppEntry =>
    buildSingleAppEntry(
      app({ key: `shop-${env}/web`, name: `web-${env}`, namespace: `shop-${env}`, identity: ident(env), versions: ['1.0.0'] }),
    )

  it('folds ≥2 same-identity instances into one group with nested instances', () => {
    const entries = [mkSibling('dev'), mkSibling('prod')]
    const collapsed = foldAppGroups(entries, new Set(), false)
    expect(collapsed).toHaveLength(1)
    expect(collapsed[0].kind).toBe('group')
    if (collapsed[0].kind === 'group') {
      expect(collapsed[0].members).toHaveLength(2)
      expect(collapsed[0].cells.map((c) => c.env).sort()).toEqual(['dev', 'prod'])
    }

    const expanded = foldAppGroups(entries, new Set(['shop']), false)
    // group row + two instance children
    expect(expanded).toHaveLength(3)
    expect(expanded.filter((r) => r.kind === 'instance')).toHaveLength(2)
  })

  it('renders a lone surviving member as a flat instance, not a group', () => {
    const out = foldAppGroups([mkSibling('dev')], new Set(), false)
    expect(out).toHaveLength(1)
    expect(out[0].kind).toBe('instance')
  })

  it('auto-expands every group when the search flag is set', () => {
    const out = foldAppGroups([mkSibling('dev'), mkSibling('prod')], new Set(), true)
    expect(out[0].kind).toBe('group')
    if (out[0].kind === 'group') expect(out[0].expanded).toBe(true)
    expect(out.filter((r) => r.kind === 'instance')).toHaveLength(2)
  })

  it('builds the env ladder from envsOf (per-cluster slices), not identity.env', () => {
    // A fleet member can span several per-cluster envs; envsOf supplies them so
    // the ladder shows every env, including ones that are not the member's own
    // identity.env (which the hub can stale on a same-key cross-cluster join).
    const a = mkSibling('dev')
    const b = mkSibling('prod')
    const envsOf = (e: SingleAppEntry) =>
      e.env === 'dev'
        ? [{ env: 'dev', health: 'healthy' as const }, { env: 'staging', health: 'degraded' as const }]
        : [{ env: 'prod', health: 'healthy' as const }]
    const out = foldAppGroups([a, b], new Set(), false, { envsOf })
    expect(out[0].kind).toBe('group')
    if (out[0].kind === 'group') {
      expect(out[0].cells.map((c) => c.env).sort()).toEqual(['dev', 'prod', 'staging'])
    }
  })
})

describe('ApplicationsView copy', () => {
  it.each([
    [undefined, '运行中 · 2'],
    ['zh-CN', '运行中 · 2'],
    ['en', 'Running · 2'],
  ])('shows the running-job count with the expected language for %s', (locale, running) => {
    const entry = buildSingleAppEntry(app({
      name: 'invoice-reconciler',
      workload_class: 'job',
      workloads: [wl({
        kind: 'CronJob',
        workload_class: 'job',
        batch: { activeRuns: 2, latestRunPhase: 'Running' },
      })],
    }))
    const view = <ApplicationsView entries={[entry]} variant="single" onSelect={() => undefined} />
    const preference = vi.spyOn(Storage.prototype, 'getItem').mockReturnValue(locale ?? null)
    try {
      const html = renderToStaticMarkup(locale ? <LocaleProvider>{view}</LocaleProvider> : view)
      expect(html).toContain(running)
      expect(html).toContain('invoice-reconciler')
      expect(entry.workloadClass).toBe('job')
    } finally {
      preference.mockRestore()
    }
  })

  const identity = { key: 'registry-credential', env: 'prod', confidence: 'high', evidence: 'declared source path' }
  const siblings = ['registry-credential-a', 'registry-credential-b'].map((name) =>
    buildSingleAppEntry(app({
      key: `secure/${name}`,
      name,
      namespace: 'secure',
      identity,
      versions: ['v1.2.3'],
      workloads: [wl({ kind: 'Secret', namespace: 'secure', name })],
    })),
  )

  it('renders localized search and family counts while preserving raw identity and kind values', () => {
    const html = renderToStaticMarkup(
      <ApplicationsView entries={siblings} variant="single" onSelect={() => undefined} />,
    )
    expect(html).toContain('搜索…（按 /）')
    expect(html).toContain('2 个实例')
    expect(html).toContain('1 个环境')
    expect(html).not.toContain('Search... (press /)')
    expect(html).not.toContain('instances')
    expect(html).not.toContain('envs')
    expect(html).toContain('registry-credential')
    expect(html).toContain('Secret')
    expect(CLASS_META.unknown.tooltip).toContain('无法根据工作负载推断运行类别')

    const search = renderToStaticMarkup(
      <SearchBox value="api" onChange={() => undefined} scope="applications" shortcutId="applications-search-test" />,
    )
    expect(search).toContain('aria-label="清除搜索"')
  })

  it('localizes the expanded environment tooltip without translating environment or version values', async () => {
    if (!window.requestAnimationFrame) {
      window.requestAnimationFrame = (callback) => window.setTimeout(() => callback(0), 0)
      window.cancelAnimationFrame = (handle) => window.clearTimeout(handle)
    }
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    act(() => {
      root.render(<ApplicationsView entries={siblings} variant="single" onSelect={() => undefined} />)
    })
    const envButton = [...host.querySelectorAll('button')].find((button) => button.textContent?.trim() === 'prod')
    expect(envButton).toBeDefined()
    act(() => {
      envButton!.parentElement!.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 180))
    })
    expect(document.body.textContent).toContain('prod · v1.2.3 · 2 个实例，展开选择')
    expect(document.body.textContent).not.toContain('instances — expand to choose')
    act(() => root.unmount())
    host.remove()
    document.body.innerHTML = ''
  })

  it('localizes the overflow-environment tooltip', async () => {
    const manyEnvironments = ['dev', 'staging', 'prod', 'qa', 'preprod'].map((env) =>
      buildSingleAppEntry(app({
        key: `api/${env}`,
        name: `api-${env}`,
        namespace: env,
        identity: { key: 'api', env, confidence: 'high', evidence: 'declared source path' },
        versions: ['v1.2.3'],
      })),
    )
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    act(() => {
      root.render(<ApplicationsView entries={manyEnvironments} variant="single" onSelect={() => undefined} />)
    })
    const overflowButton = [...host.querySelectorAll('button')].find((button) => button.textContent?.trim() === '+1')
    expect(overflowButton).toBeDefined()
    act(() => {
      overflowButton!.parentElement!.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 180))
    })
    expect(document.body.textContent).toContain('1 个环境，展开查看全部实例')
    expect(host.textContent).toContain('api')
    act(() => root.unmount())
    host.remove()
    document.body.innerHTML = ''
  })

  it('localizes cluster counts while preserving cluster names', async () => {
    const base = buildSingleAppEntry(app({ name: 'registry-credential', namespace: 'secure' }))
    const fleetEntry: FleetAppEntry = {
      ...base,
      variant: 'fleet',
      envs: [{ env: 'prod', health: 'healthy', inferred: false }],
      clusters: Array.from({ length: 6 }, (_, index) => ({
        id: `cluster-${index}`,
        name: `cluster-${index}`,
        health: 'healthy' as const,
      })),
      versionSkew: false,
    }
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    act(() => {
      root.render(<ApplicationsView entries={[fleetEntry]} variant="fleet" onSelect={() => undefined} />)
    })
    expect(host.textContent).toContain('6 个集群')
    expect(host.textContent).not.toContain('6 clusters')
    const clusterCount = [...host.querySelectorAll('span')].find((node) => node.textContent?.trim() === '6 个集群')
    expect(clusterCount).toBeDefined()
    act(() => {
      clusterCount!.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 180))
    })
    expect(document.body.textContent).toContain('cluster-0')
    expect(document.body.textContent).toContain('cluster-5')
    act(() => root.unmount())
    host.remove()
    document.body.innerHTML = ''
  })
})
