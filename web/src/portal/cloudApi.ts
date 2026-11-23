import { useQuery } from '@tanstack/react-query'
import type { Translate } from '@maomada/k8s-ui/hooks/useLocale'
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

export class LocalizedCloudError extends Error {
  constructor(message: string, readonly englishMessage?: string, options?: ErrorOptions) {
    super(message, options)
  }
}

export class CloudRequestError extends LocalizedCloudError {
  constructor(message: string, readonly status: number, readonly code?: string, readonly requestId?: string, englishMessage?: string) {
    super(message, englishMessage)
    this.name = 'CloudRequestError'
  }
}

export function cloudErrorMessage(error: unknown, t: Translate) {
  if (!(error instanceof Error)) return t('数据不可用', 'Data unavailable')
  if (error instanceof LocalizedCloudError && error.englishMessage) return t(error.message, error.englishMessage)
  return error.message
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
  const requestUrl = apiUrl(path, base)
  const requestInit: RequestInit = {
    method,
    credentials: getCredentialsMode(),
    cache: 'no-store',
    redirect: 'error',
    headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(20000)]) : AbortSignal.timeout(20000),
  }
  let response: Response
  try {
    response = await fetch(requestUrl, requestInit)
  } catch (failure) {
    if (signal?.aborted || (failure instanceof Error && failure.name === 'AbortError')) throw failure
    if (failure instanceof TypeError) {
      throw new LocalizedCloudError('网络请求失败，请检查网络连接后重试。', 'Network request failed. Check your connection and try again.', { cause: failure })
    }
    throw failure
  }
  if (!response.ok) {
    const error: unknown = response.headers.get('Content-Type')?.includes('application/json')
      ? await response.json().catch(() => null) : null
    const detail = error && typeof error === 'object' ? error as Record<string, unknown> : {}
    const message = typeof detail.message === 'string' ? detail.message
      : typeof detail.error === 'string' ? detail.error : undefined
    if (response.status === 401 && base === '/api') window.location.replace('/login?expired=1')
    throw new CloudRequestError(message ?? `请求失败（HTTP ${response.status}）`, response.status,
      typeof detail.error_code === 'string' ? detail.error_code : undefined,
      typeof detail.requestId === 'string' ? detail.requestId : undefined,
      message === undefined ? `Request failed (HTTP ${response.status})` : undefined)
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
