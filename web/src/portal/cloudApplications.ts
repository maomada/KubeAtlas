import { buildSingleAppEntry, worstHealth, type AppRow, type FleetAppEntry } from '@maomada/k8s-ui/utils/applications'
import type { ClusterResult } from './cloudFleet'
import { clusterPage } from './cloudApi'

export interface ApplicationTarget { clusterId: string; clusterName: string; href: string }

export function buildFleetApplications(results: ClusterResult<{ applications: AppRow[] }>[]) {
  const groups = new Map<string, { app: AppRow; cluster: ClusterResult<unknown>['cluster'] }[]>()
  for (const result of results) for (const app of result.data?.applications ?? []) {
    const group = groups.get(app.key) ?? []
    group.push({ app, cluster: result.cluster })
    groups.set(app.key, group)
  }
  const targets = new Map<string, ApplicationTarget[]>()
  const entries: FleetAppEntry[] = []
  for (const [key, instances] of groups) {
    const singles = instances.map(({ app }) => buildSingleAppEntry(app))
    const workloads = instances.flatMap(({ app }) => app.workloads ?? [])
    const namespaces = [...new Set(workloads.map(workload => workload.namespace))].sort()
    const versionsByImage = new Map<string, Set<string>>()
    for (const workload of workloads) {
      if (!workload.image || !workload.version) continue
      const repository = workload.image.replace(/@.*$/, '').replace(/:[^/:]+$/, '')
      const versions = versionsByImage.get(repository) ?? new Set<string>()
      versions.add(workload.version)
      versionsByImage.set(repository, versions)
    }
    const versionSkew = instances.some(({ app }) => app.versionSkew) || [...versionsByImage.values()].some(versions => versions.size > 1)
    const identities = instances.map(({ app }) => app.identity)
    const identityAgrees = identities.every(identity => identity?.key === identities[0]?.key && identity?.pathKey === identities[0]?.pathKey && identity?.portable === identities[0]?.portable)
    const row: AppRow = {
      ...instances[0].app,
      workloads,
      namespaces,
      namespace: namespaces.length === 1 ? namespaces[0] : undefined,
      identity: identityAgrees ? identities[0] : undefined,
      health: worstHealth(singles.map(single => single.health)),
      category: singles.every(single => single.category === singles[0].category) ? singles[0].category : 'mixed',
      workload_class: singles.every(single => single.workloadClass === singles[0].workloadClass) ? singles[0].workloadClass : 'mixed',
      versions: [...new Set(instances.flatMap(({ app }) => app.versions ?? []))],
      versionSkew,
      appVersion: instances.every(({ app }) => app.appVersion === instances[0].app.appVersion) ? instances[0].app.appVersion : undefined,
    }
    const summary = buildSingleAppEntry(row)
    const envs = new Map<string, FleetAppEntry['envs'][number]>()
    singles.forEach((single, index) => {
      const env = instances[index].cluster.environment || single.env
      const existing = envs.get(env)
      envs.set(env, { env, health: existing ? worstHealth([existing.health, single.health]) : single.health, inferred: !instances[index].cluster.environment && single.envInferred })
    })
    entries.push({ ...summary, variant: 'fleet', health: row.health as FleetAppEntry['health'], envs: [...envs.values()], clusters: instances.map(({ cluster }, index) => ({ id: cluster.id, name: cluster.name, health: singles[index].health })), versionSkew })
    targets.set(key, instances.map(({ app, cluster }) => ({ clusterId: cluster.id, clusterName: cluster.name, href: clusterPage(cluster.id, `/applications?${new URLSearchParams({ app: app.key })}`) })))
  }
  return { entries, targets }
}
