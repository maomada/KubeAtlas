import { useQuery } from '@tanstack/react-query'
import type { IssuesResponse } from '../api/client'
import { cloudRequest, clusterBase, CloudRequestError, type HubCluster } from './cloudApi'

export interface ClusterResult<T> { cluster: HubCluster; data?: T; error?: Error }

export function issueCoverageComplete(data: IssuesResponse): boolean {
  return (!data.visibility || data.visibility.state === 'ok') &&
    !data.unchecked_sources?.length &&
    (data.total_matched ?? 0) <= (data.total ?? data.issues.length)
}

export async function fetchFleet<T>(clusters: HubCluster[], path: string, signal: AbortSignal): Promise<ClusterResult<T>[]> {
  const results: ClusterResult<T>[] = new Array(clusters.length)
  let next = 0
  await Promise.all(Array.from({ length: Math.min(4, clusters.length) }, async () => {
    while (next < clusters.length) {
      signal.throwIfAborted()
      const index = next++
      const cluster = clusters[index]
      if (cluster.state !== 'connected') {
        results[index] = { cluster, error: new CloudRequestError(cluster.revokedAt ? 'Agent access revoked' : 'Agent disconnected', 503, 'cluster_unavailable') }
        continue
      }
      try { results[index] = { cluster, data: await cloudRequest<T>(path, { base: clusterBase(cluster.id), signal }) } }
      catch (error) {
        signal.throwIfAborted()
        results[index] = { cluster, error: error instanceof Error ? error : new Error('Cluster request failed') }
      }
    }
  }))
  return results
}

export function useFleetQuery<T>(clusters: HubCluster[], path: string, enabled = true) {
  return useQuery({
    queryKey: ['portal-fleet', clusters.map(cluster => `${cluster.id}:${cluster.state}`), path],
    queryFn: ({ signal }) => fetchFleet<T>(clusters, path, signal),
    enabled,
    retry: false,
    staleTime: 10000,
    refetchInterval: 30000,
  })
}
