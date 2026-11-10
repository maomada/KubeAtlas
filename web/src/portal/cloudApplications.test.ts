import { describe, expect, it } from 'vitest'
import { foldAppGroups, type AppRow } from '@maomada/k8s-ui/utils/applications'
import { buildFleetApplications } from './cloudApplications'
import type { HubCluster } from './cloudApi'

const cluster = (id: string): HubCluster => ({ id, name: id, description: '', environment: '', region: '', createdAt: '', updatedAt: '', configured: true, state: 'connected' })
const app = (key: string, image = 'example/app:1', version = '1'): AppRow => ({
  key, name: 'App', health: 'healthy', workload_class: 'service', versions: [version],
  workloads: [{ kind: 'Deployment', namespace: 'shop', name: 'web', image, version, health: 'healthy', ready: 2, desired: 2, restarts: 0 }],
})

describe('fleet application grouping', () => {
  it('joins structural keys, keeps both destinations and detects same-image version skew', () => {
    const result = buildFleetApplications([
      { cluster: cluster('east'), data: { applications: [app('helm:shop/web')] } },
      { cluster: cluster('west'), data: { applications: [app('helm:shop/web', 'example/app:2', '2')] } },
    ])
    expect(result.entries).toHaveLength(1)
    expect(result.entries[0]).toMatchObject({ ready: 4, desired: 4, versionSkew: true })
    expect(result.entries[0].clusters.map(cluster => cluster.id)).toEqual(['east', 'west'])
    expect(result.targets.get('helm:shop/web')?.map(target => target.href)).toEqual([
      '/c/east/applications?app=helm%3Ashop%2Fweb', '/c/west/applications?app=helm%3Ashop%2Fweb',
    ])
  })

  it('does not treat different component images as version skew or failed clusters as healthy', () => {
    const result = buildFleetApplications([
      { cluster: cluster('east'), data: { applications: [app('helm:shop/web')] } },
      { cluster: cluster('west'), data: { applications: [app('helm:shop/web', 'example/worker:7', '7')] } },
      { cluster: cluster('unavailable'), error: new Error('denied') },
    ])
    expect(result.entries[0].versionSkew).toBe(false)
    expect(result.entries[0].clusters).toHaveLength(2)
    expect(result.targets.get('helm:shop/web')).toHaveLength(2)
  })

  it('folds declared portable identities but keeps name-only identities scoped to their clusters', () => {
    const rows = (portable: boolean) => ['east', 'west'].map(id => ({ cluster: cluster(id), data: { applications: [{ ...app(`argo:shop/${id}`), identity: { key: 'shop', env: id, confidence: 'high', evidence: 'declared', portable } }] } }))
    const folded = (portable: boolean) => foldAppGroups(buildFleetApplications(rows(portable)).entries, new Set(), false, { localScope: entry => entry.clusters.map(cluster => cluster.id).sort().join(',') })
    expect(folded(false)).toHaveLength(2)
    expect(folded(true)).toHaveLength(1)
    expect(folded(true)[0].kind).toBe('group')
  })
})
