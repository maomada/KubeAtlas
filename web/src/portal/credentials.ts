import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { safeExternalURL } from './model'

export interface MCPCredential {
  id: string
  label: string
  clusterIds: string[]
  readOnly: boolean
  createdAt: string
  expiresAt: string
  refreshUntil: string
  revokedAt?: string
}

export interface IssuedCredential {
  credential: MCPCredential
  token: string
}

export interface CredentialCluster {
  id: string
  name: string
  revokedAt?: string
}

class ManagementRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'ManagementRequestError'
  }
}

export function credentialStatus(credential: MCPCredential, now: number): 'active' | 'expired' | 'revoked' | 'unknown' {
  if (credential.revokedAt) return 'revoked'
  const expires = Date.parse(credential.expiresAt)
  if (!Number.isFinite(expires) || !Number.isFinite(now)) return 'unknown'
  return expires <= now ? 'expired' : 'active'
}

export function openCodeFleetConfig(endpoint: string): string {
  const url = safeExternalURL(endpoint)
  if (!url) throw new Error('请输入不含凭据、查询参数或片段的 HTTP / HTTPS 地址。')
  return JSON.stringify({
    mcp: {
      'kubeatlas-fleet': {
        type: 'remote', url, oauth: false,
        headers: { Authorization: 'Bearer {env:KUBEATLAS_MCP_TOKEN}' },
      },
    },
  }, null, 2)
}

export async function credentialsRequest<T>(path = '', method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  return managementRequest<T>(`/mcp/credentials${path}`, method, body, signal)
}

export async function credentialClusters(signal: AbortSignal): Promise<CredentialCluster[]> {
  const data = await managementRequest<{ clusters: CredentialCluster[] }>('/hub/clusters', 'GET', undefined, signal)
  if (!Array.isArray(data.clusters) || !data.clusters.every(cluster => cluster && typeof cluster.id === 'string' && typeof cluster.name === 'string')) {
    throw new Error('门户返回的 Hub 集群目录无效。')
  }
  return data.clusters.filter(cluster => !cluster.revokedAt)
}

export async function hubAvailability(signal: AbortSignal): Promise<boolean> {
  try {
    const data = await managementRequest<unknown>('/hub', 'GET', undefined, signal)
    if (!data || typeof data !== 'object' || !('enabled' in data) || typeof data.enabled !== 'boolean') {
      throw new Error('门户返回了无效的 Hub 状态。')
    }
    return data.enabled
  } catch (error) {
    if (error instanceof ManagementRequestError && error.status === 404) return false
    throw error
  }
}

async function managementRequest<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(apiUrl(path), {
    method, credentials: getCredentialsMode(), cache: 'no-store',
    headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000),
  })
  if (response.status === 401) {
    window.location.replace('/login?expired=1')
    throw new ManagementRequestError('门户登录已失效，请重新登录；MCP 凭据有独立的有效期。', response.status)
  }
  if (!response.ok) {
    if (path === '/hub/clusters' && response.status === 404) throw new Error('请确认 Portal 已启用 Hub 模式。')
    let message = `请求失败（HTTP ${response.status}）`
    if (response.headers.get('Content-Type')?.includes('application/json')) {
      const error = await response.json().catch(() => undefined)
      if (error && typeof error === 'object') {
        if ('message' in error && typeof error.message === 'string') message = error.message
        else if ('error' in error && typeof error.error === 'string') message = error.error
      }
    }
    throw new ManagementRequestError(message, response.status)
  }
  return response.status === 204 ? undefined as T : await response.json() as T
}
