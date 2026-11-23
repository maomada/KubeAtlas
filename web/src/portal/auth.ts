import type { Translate } from '@maomada/k8s-ui/hooks/useLocale'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'

const defaultTranslate: Translate = chinese => chinese

export interface AuthProviders {
  registrationEnabled: boolean
  oidcEnabled: boolean
}

export async function probePortalSession(signal?: AbortSignal, t: Translate = defaultTranslate): Promise<'authenticated' | 'unauthenticated'> {
  const response = await fetch(apiUrl('/auth/session', '/api'), {
    headers: getAuthHeaders(), credentials: getCredentialsMode(), cache: 'no-store', redirect: 'error',
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000),
  })
  if (response.status === 401) return 'unauthenticated'
  if (!response.ok) throw new Error(t(`会话检查失败（HTTP ${response.status}）`, `Session check failed (HTTP ${response.status}).`))
  return 'authenticated'
}

export async function fetchAuthProviders(signal?: AbortSignal, t: Translate = defaultTranslate): Promise<AuthProviders> {
  const response = await fetch(apiUrl('/auth/providers', '/api'), {
    headers: getAuthHeaders(), credentials: getCredentialsMode(), cache: 'no-store', redirect: 'error',
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000),
  })
  if (response.status === 404) return { registrationEnabled: false, oidcEnabled: false }
  if (!response.ok) throw new Error(t('认证选项不可用。', 'Sign-in options are unavailable.'))
  const value: unknown = await response.json()
  if (!value || typeof value !== 'object') throw new Error(t('认证选项无效。', 'The portal returned invalid sign-in options.'))
  const providers = value as Record<string, unknown>
  const oidc = providers.oidc && typeof providers.oidc === 'object' ? providers.oidc as Record<string, unknown> : undefined
  return { registrationEnabled: providers.registrationEnabled === true, oidcEnabled: oidc?.enabled === true }
}
