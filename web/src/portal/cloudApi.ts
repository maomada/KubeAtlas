import { useQuery } from '@tanstack/react-query'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'

export interface HubCluster {
  id: string
  organizationId: string
  name: string
  description: string
  environment: string
  region: string
  createdAt: string
  updatedAt: string
  revokedAt?: string
  configured: boolean
  state: 'connected' | 'offline' | 'revoked'
  agent?: {
    clusterId: string
    version: string
    connectedAt: string
    connectionId: string
  }
}

export interface HubGroup {
  id: string
  name: string
  clusterIds: string[]
}

export class CloudRequestError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string, readonly requestId?: string) {
    super(message)
    this.name = 'CloudRequestError'
  }
}

export function clusterBase(clusterId: string): string {
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(clusterId)) throw new Error('集群 ID 无效')
  return `/c/${encodeURIComponent(clusterId)}/api`
}

export async function cloudRequest<T>(path: string, options: {
  method?: string
  body?: unknown
  signal?: AbortSignal
  base?: string
} = {}): Promise<T> {
  const { method = 'GET', body, signal, base = '/api' } = options
  const response = await fetch(apiUrl(path, base), {
    method,
    credentials: getCredentialsMode(),
    cache: 'no-store',
    redirect: 'error',
    headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(20000)]) : AbortSignal.timeout(20000),
  })
  if (!response.ok) {
    const error: unknown = response.headers.get('Content-Type')?.includes('application/json')
      ? await response.json().catch(() => null) : null
    const detail = error && typeof error === 'object' ? error as Record<string, unknown> : {}
    const message = typeof detail.message === 'string' ? detail.message
      : typeof detail.error === 'string' ? detail.error : `请求失败（HTTP ${response.status}）`
    if (response.status === 401 && base === '/api') window.location.replace('/login?expired=1')
    throw new CloudRequestError(message, response.status,
      typeof detail.error_code === 'string' ? detail.error_code : undefined,
      typeof detail.requestId === 'string' ? detail.requestId : undefined)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export function useCloudQuery<T>(path: string, base = '/api', enabled = true) {
  return useQuery({
    queryKey: ['portal', base, path],
    queryFn: ({ signal }) => cloudRequest<T>(path, { base, signal }),
    enabled,
    retry: false,
    staleTime: 5000,
    refetchInterval: 15000,
  })
}

export function clusterPage(clusterId: string, path = '/'): string {
  clusterBase(clusterId)
  return `/c/${encodeURIComponent(clusterId)}${path === '/' ? '' : path}`
}

export function selectedCluster(pathname: string): string | undefined {
  const match = /^\/c\/([^/]+)(?:\/|$)/.exec(pathname)
  if (!match) return undefined
  try {
    const id = decodeURIComponent(match[1])
    clusterBase(id)
    return id
  } catch {
    return undefined
  }
}

export function clusterView(pathname: string): string {
  return pathname.replace(/^\/c\/[^/]+/, '') || '/'
}
