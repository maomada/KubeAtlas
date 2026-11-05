import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'

export interface FleetDocument {
  tool: string
  input: Record<string, unknown>
  data?: unknown
  error?: string
}

export interface SnapshotContext {
  clusterId: string
  name?: string
  observedAt: string
  receivedAt: string
  state: string
  documents: FleetDocument[]
  limitations: string[]
}

export interface FleetGroup {
  id: string
  name: string
  clusterIds: string[]
}

export interface AgentContext {
  mode: string
  fleet?: FleetGroup
  clusters: SnapshotContext[]
  limitations: string[]
  federation?: {
    state: string
    truncated: boolean
    relationships: { type: string; state: string; source: { clusterId: string; resource: { kind: string; name: string } }; targets: { clusterId: string }[]; evidence: unknown }[]
    coverage: { clusterId: string; state: string; facts: number }[]
    limitations: string[]
  }
}

export interface ClusterTools {
  clusterId: string
  state: string
  tools: { name: string; description?: string; inputSchema: unknown }[]
}

export async function fleetRequest<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(apiUrl(`/fleet${path}`), {
    method, credentials: getCredentialsMode(), cache: 'no-store',
    headers: { ...getAuthHeaders(), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000),
  })
  if (response.status === 401) {
    window.location.replace('/login?expired=1')
    throw new Error('门户登录会话已过期，请重新登录。')
  }
  if (!response.ok) {
    let message = `请求失败（HTTP ${response.status}）`
    const contentType = response.headers.get('Content-Type') || ''
    if (contentType.includes('application/json')) {
      const error = await response.json()
      if (typeof error.error === 'string') message = error.error
    }
    throw new Error(message)
  }
  return response.status === 204 ? undefined as T : await response.json() as T
}

export function resourceItems(document: FleetDocument): { name: string; namespace: string }[] {
  if (document.tool !== 'list_resources' || !document.data || typeof document.data !== 'object') return []
  const data = document.data as Record<string, unknown>
  if (!Array.isArray(data.items)) return []
  return data.items.flatMap(item => {
    if (!item || typeof item !== 'object' || typeof item.name !== 'string') return []
    return [{ name: item.name, namespace: typeof item.namespace === 'string' ? item.namespace : '' }]
  })
}
