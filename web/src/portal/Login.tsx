import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { ArrowRight, Layers, LoaderCircle, LockKeyhole, Plug, Globe, ShieldCheck } from 'lucide-react'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { PortalHeaderActions } from './PortalLanguageSelect'
import { fetchAuthProviders } from './auth'

export function Login() {
  const { t } = useLocale()
  useEffect(() => { document.title = t('登录 · KubeAtlas', 'Sign in · KubeAtlas') }, [t])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [ssoEnabled, setSSOEnabled] = useState(false)
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [providerError, setProviderError] = useState(false)
  const expired = new URLSearchParams(window.location.search).get('expired') === '1'
  const ssoFailed = new URLSearchParams(window.location.search).get('error') === 'sso_failed'

  useEffect(() => {
    const controller = new AbortController()
    fetchAuthProviders(controller.signal)
      .then(providers => {
        if (!controller.signal.aborted) {
          setSSOEnabled(providers.oidcEnabled)
          setRegistrationEnabled(providers.registrationEnabled)
        }
      })
      .catch(() => { if (!controller.signal.aborted) setProviderError(true) })
    return () => controller.abort()
  }, [])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const response = await fetch(apiUrl('/auth/login', '/api'), {
        method: 'POST', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' },
        credentials: getCredentialsMode(), redirect: 'error', body: JSON.stringify({ username, password }),
        signal: AbortSignal.timeout(10000),
      })
      if (!response.ok) {
        if (response.status === 401) throw new Error(t('账号或密码不正确，请重新输入。', 'Incorrect username or password. Try again.'))
        if (response.status === 429) throw new Error(t('登录尝试过于频繁，请稍后再试。', 'Too many sign-in attempts. Try again later.'))
        throw new Error(t('暂时无法登录，请稍后重试。', 'Unable to sign in. Try again later.'))
      }
      const session = await fetch(apiUrl('/auth/session', '/api'), {
        headers: getAuthHeaders(), credentials: getCredentialsMode(), cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(10000),
      })
      if (!session.ok) throw new Error(t('无法建立登录会话，请重新登录。', 'Unable to start a session. Sign in again.'))
      window.location.replace('/')
    } catch (cause) {
      setError(cause instanceof Error && cause.name === 'Error' ? cause.message : t('连接门户失败，请检查网络后重试。', 'Unable to connect to the portal. Check your connection and try again.'))
    } finally {
      setPassword('')
      setBusy(false)
    }
  }

  return <div className="flex min-h-screen flex-col">
    <header className="border-b border-theme-border bg-theme-surface">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-2 px-3 py-5 sm:px-6 lg:px-10">
        <a href="/login" aria-label={t('KubeAtlas 门户登录首页', 'KubeAtlas portal sign-in')} className="flex items-center gap-2 sm:gap-3"><Globe size={29} className="shrink-0 text-accent-text" /><span className="text-lg font-semibold sm:text-xl">KubeAtlas</span><span className="hidden border-l border-theme-border pl-3 text-sm text-theme-text-secondary sm:inline">{t('集群门户', 'Cluster portal')}</span></a>
        <PortalHeaderActions />
      </div>
    </header>
    <main className="mx-auto grid w-full max-w-6xl flex-1 content-center gap-10 px-6 py-12 lg:grid-cols-2 lg:items-center lg:gap-20 lg:px-10 lg:py-20">
      <section aria-label={t('门户介绍', 'About the portal')}>
        <div className="mb-6 inline-flex rounded-2xl border border-theme-border bg-theme-surface p-4 text-accent-text"><Globe size={36} /></div>
        <p className="portal-mono mb-3 text-xs text-accent-text">{t('KubeAtlas 集群门户', 'KubeAtlas cluster portal')}</p>
        <h1 className="text-3xl font-semibold leading-tight lg:text-4xl">{t('一个入口，', 'One place to')}<br />{t('连接所有 KubeAtlas。', 'connect your KubeAtlas clusters.')}</h1>
        <p className="mt-5 max-w-md text-base leading-relaxed text-theme-text-secondary">{t('登录集中式门户，查看集群状态、管理组织访问，为 AI Agent 提供统一入口。', 'Sign in to view cluster status, manage organization access, and connect AI agents.')}</p>
        <div className="mt-8 hidden space-y-5 lg:block">
          {[
            { icon: Layers, title: t('统一实例目录', 'Instance directory'), detail: t('本地与集群内 KubeAtlas，按环境与心跳状态检索。', 'Find local and in-cluster KubeAtlas instances by environment and connection status.') },
            { icon: Plug, title: t('多集群与 MCP', 'Multiple clusters and MCP'), detail: t('统一入口，明确选择目标集群及访问范围。', 'Choose target clusters and access scope from one place.') },
            { icon: ShieldCheck, title: t('独立认证边界', 'Separate access boundaries'), detail: t('组织访问授权与目标集群的 Kubernetes RBAC 分别生效。', 'Organization access and each cluster’s Kubernetes RBAC are enforced independently.') },
          ].map(item => <div key={item.title} className="flex items-start gap-3"><item.icon size={20} className="mt-0.5 shrink-0 text-accent-text" /><div><p className="text-sm font-medium">{item.title}</p><p className="mt-1 text-sm text-theme-text-secondary">{item.detail}</p></div></div>)}
        </div>
      </section>
      <section aria-labelledby="login-title" className="portal-card p-6 shadow-theme-sm sm:p-9">
        <div className="mb-5 inline-flex rounded-xl bg-accent-muted p-3 text-accent-text"><LockKeyhole size={23} /></div>
        <h2 id="login-title" className="text-2xl font-semibold">{t('登录集群门户', 'Sign in to the cluster portal')}</h2>
        <p className="mt-2 text-sm leading-relaxed text-theme-text-secondary">{t('使用用户名或邮箱登录。', 'Sign in with your username or email.')}</p>
        {ssoEnabled && <div className="mt-6 space-y-3"><a href={apiUrl('/auth/oidc/login', '/api')} className="btn-brand flex w-full items-center justify-center gap-2 py-3 text-sm"><ShieldCheck size={17} />{t('通过组织 SSO 登录', 'Sign in with organization SSO')}</a><p className="text-xs text-theme-text-tertiary">{t('使用独立自托管身份服务；首次登录需要管理员审批。', 'Uses a self-hosted identity provider. An administrator must approve first-time sign-in.')}</p><p className="border-t border-theme-border pt-4 text-xs text-theme-text-secondary">{t('或使用本地管理员恢复账号', 'Or use the local administrator recovery account')}</p></div>}
        {(providerError || ssoFailed) && <p role="alert" className="mt-5 text-sm text-warning-text">{providerError ? t('登录方式暂时无法读取。本地恢复账号仍可登录。', 'Sign-in options could not be loaded. The local recovery account is still available.') : t('SSO 登录未完成或账号不可用。请重试，或联系组织管理员。', 'SSO sign-in did not complete or your account is unavailable. Try again or contact your organization administrator.')}</p>}
        {expired && !error && <p role="status" className="mt-5 rounded-lg border border-theme-border bg-theme-base p-3 text-sm text-theme-text-secondary">{t('登录会话已过期，请重新登录。', 'Your session has expired. Sign in again.')}</p>}
        <form className="mt-7 space-y-5" onSubmit={submit} aria-busy={busy}>
          <div><label htmlFor="portal-username" className="mb-2 block text-sm font-medium">{t('用户名或邮箱', 'Username or email')}</label><input id="portal-username" name="username" autoComplete="username" className="portal-control w-full" placeholder={t('请输入用户名或邮箱', 'Enter your username or email')} value={username} onChange={event => setUsername(event.target.value)} required maxLength={256} disabled={busy} autoCapitalize="none" spellCheck={false} /></div>
          <div><label htmlFor="portal-password" className="mb-2 block text-sm font-medium">{t('密码', 'Password')}</label><input id="portal-password" name="password" type="password" autoComplete="current-password" className="portal-control w-full" placeholder={t('请输入密码', 'Enter your password')} value={password} onChange={event => setPassword(event.target.value)} required maxLength={4096} disabled={busy} /></div>
          {error && <p role="alert" className="rounded-lg border border-theme-border bg-theme-base p-3 text-sm text-theme-text-secondary">{error}</p>}
          <button type="submit" className="btn-brand flex w-full items-center justify-center gap-2 py-3 text-sm font-medium" disabled={busy}>{busy ? <LoaderCircle size={17} className="animate-spin" /> : null}{busy ? t('正在登录…', 'Signing in…') : t('登录', 'Sign in')}{!busy && <ArrowRight size={17} />}</button>
        </form>
        {registrationEnabled && <p className="mt-5 text-sm text-theme-text-secondary">{t('还没有账号？', 'Need an account?')} <a className="text-accent-text" href="/register">{t('注册', 'Register')}</a></p>}
      </section>
    </main>
    <footer className="px-6 pb-6 text-center text-xs text-theme-text-tertiary">{t('KubeAtlas Portal · 独立部署，集中访问', 'KubeAtlas Portal · Self-hosted cluster access')}</footer>
  </div>
}
