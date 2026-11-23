import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { LocalizedCloudError } from './cloudApi'

export interface MCPCredential {
  id: string
  organizationId: string
  label: string
  clusterIds: string[]
  readOnly: boolean
  createdAt: string
  expiresAt?: string
  refreshUntil?: string
  neverExpires?: boolean
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

class ManagementRequestError extends LocalizedCloudError {
  constructor(message: string, readonly status: number, englishMessage?: string) {
    super(message, englishMessage)
    this.name = 'ManagementRequestError'
  }
}

export function credentialStatus(credential: MCPCredential, now: number): 'active' | 'expired' | 'revoked' | 'unknown' {
  if (credential.revokedAt) return 'revoked'
  if (credential.neverExpires === true) return 'active'
  const expires = Date.parse(credential.expiresAt ?? '')
  if (!Number.isFinite(expires) || !Number.isFinite(now)) return 'unknown'
  return expires <= now ? 'expired' : 'active'
}

function safeExternalURL(value: string): string | undefined {
  try {
    const url = new URL(value)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || /[\s\\]/.test(value)) return undefined
    return url.href
  } catch {
    return undefined
  }
}

export function mcpConnectionInfo(endpoint: string, token: string): string {
  const url = safeExternalURL(endpoint)
  if (!url) throw new LocalizedCloudError(
    '请输入不含凭据、查询参数或片段的 HTTP / HTTPS 地址。',
    'Enter an HTTP or HTTPS URL without credentials, a query, or a fragment.',
  )
  if (!token || /\s/.test(token) || [...token].some(character => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)) {
    throw new LocalizedCloudError('MCP Token 无效。', 'Invalid MCP token.')
  }
  return `Transport: Streamable HTTP\nURL: ${url}\nAuthorization: Bearer ${token}`
}

export async function credentialsRequest<T>(path = '', method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  return managementRequest<T>(`/mcp/credentials${path}`, method, body, signal)
}

export async function credentialClusters(signal: AbortSignal): Promise<CredentialCluster[]> {
  const data = await managementRequest<{ clusters: CredentialCluster[] }>('/hub/clusters', 'GET', undefined, signal)
  if (!Array.isArray(data.clusters) || !data.clusters.every(cluster => cluster && typeof cluster.id === 'string' && typeof cluster.name === 'string')) {
    throw new LocalizedCloudError('门户返回的 Hub 集群目录无效。', 'The Hub returned an invalid cluster directory.')
  }
  return data.clusters.filter(cluster => !cluster.revokedAt)
}

async function managementRequest<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(apiUrl(path, '/api'), {
    method, credentials: getCredentialsMode(), cache: 'no-store',
    headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000),
  })
  if (response.status === 401) {
    window.location.replace('/login?expired=1')
    throw new ManagementRequestError(
      '门户登录已失效，请重新登录；MCP 凭据有独立的有效期。', response.status,
      'Your Portal session has expired. Sign in again. MCP credentials have their own lifetime.',
    )
  }
  if (!response.ok) {
    let message: string | undefined
    if (response.headers.get('Content-Type')?.includes('application/json')) {
      const error = await response.json().catch(() => undefined)
      if (error && typeof error === 'object') {
        if ('message' in error && typeof error.message === 'string') message = error.message
        else if ('error' in error && typeof error.error === 'string') message = error.error
      }
    }
    throw new ManagementRequestError(
      message ?? `请求失败（HTTP ${response.status}）`, response.status,
      message === undefined ? `Request failed (HTTP ${response.status})` : undefined,
    )
  }
  return response.status === 204 ? undefined as T : await response.json() as T
}
