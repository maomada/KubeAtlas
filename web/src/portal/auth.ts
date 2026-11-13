import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'

export interface AuthProviders {
  registrationEnabled: boolean
  oidcEnabled: boolean
}

export async function probePortalSession(signal?: AbortSignal): Promise<'authenticated' | 'unauthenticated'> {
  const response = await fetch(apiUrl('/auth/session', '/api'), {
    headers: getAuthHeaders(), credentials: getCredentialsMode(), cache: 'no-store', redirect: 'error',
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000),
  })
  if (response.status === 401) return 'unauthenticated'
  if (!response.ok) throw new Error(`Session check failed (HTTP ${response.status})`)
  return 'authenticated'
}

export async function fetchAuthProviders(signal?: AbortSignal): Promise<AuthProviders> {
  const response = await fetch(apiUrl('/auth/providers', '/api'), {
    headers: getAuthHeaders(), credentials: getCredentialsMode(), cache: 'no-store', redirect: 'error',
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000),
  })
  if (response.status === 404) return { registrationEnabled: false, oidcEnabled: false }
  if (!response.ok) throw new Error('Authentication options are unavailable.')
  const value: unknown = await response.json()
  if (!value || typeof value !== 'object') throw new Error('Authentication options are invalid.')
  const providers = value as Record<string, unknown>
  const oidc = providers.oidc && typeof providers.oidc === 'object' ? providers.oidc as Record<string, unknown> : undefined
  return { registrationEnabled: providers.registrationEnabled === true, oidcEnabled: oidc?.enabled === true }
}
