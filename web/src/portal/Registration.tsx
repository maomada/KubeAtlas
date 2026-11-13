import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, Globe, LoaderCircle, ShieldCheck } from 'lucide-react'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { CloudError, CloudLoading, CloudLink, UnencryptedHttpWarning } from './CloudShared'
import { fetchAuthProviders } from './auth'
import { ThemeToggle } from './ThemeToggle'

export function PortalLanding() {
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    fetchAuthProviders(controller.signal)
      .then(providers => setRegistrationEnabled(providers.registrationEnabled))
      .catch(() => setRegistrationEnabled(false))
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])
  return <div className="portal-auth-page">
    <header className="portal-auth-header"><a href="/" aria-label="KubeAtlas 门户首页"><Globe size={28} />KubeAtlas <span>集群门户</span></a><ThemeToggle /></header>
    <main className="portal-auth-main">
      <section><p className="portal-mono text-xs text-accent-text">KubeAtlas · 自托管集群门户</p><h1>从一个入口，管理多个组织与集群</h1><p className="text-theme-text-secondary">查看已授权的集群状态，按组织管理访问，并通过显式配对流程连接新的集群。</p><div className="mt-7 flex flex-wrap gap-3"><CloudLink href="/login" className="btn-brand inline-flex items-center gap-2 px-5 py-3">登录 <ArrowRight size={16} /></CloudLink>{loading ? <span role="status">正在读取注册配置…</span> : registrationEnabled ? <CloudLink href="/register" className="cloud-button">创建账号</CloudLink> : <span className="text-sm text-theme-text-tertiary">账号注册当前不可用</span>}</div>
        <div className="mt-8 flex items-start gap-3"><ShieldCheck size={19} className="mt-0.5 shrink-0 text-accent-text" /><p className="text-sm text-theme-text-secondary">每个组织的 Hub 权限独立于 Kubernetes RBAC；只有平台所有者可以管理平台用户与组织。</p></div>
      </section>
      <section className="portal-card p-6"><h2 className="text-xl font-semibold">自托管 · 明确授权</h2><p className="mt-3 text-sm text-theme-text-secondary">门户不会自动授予集群权限。组织成员资格、平台管理与集群 Kubernetes 权限分别执行。</p><UnencryptedHttpWarning /></section>
    </main>
    <footer className="portal-auth-footer">KubeAtlas Portal · 独立部署，集中访问</footer>
  </div>
}

export function Register() {
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [providerError, setProviderError] = useState<unknown>()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  const [submitted, setSubmitted] = useState(false)
  const passwordCodepoints = Array.from(password).length
  const passwordBytes = new TextEncoder().encode(password).length
  useEffect(() => {
    const controller = new AbortController()
    fetchAuthProviders(controller.signal)
      .then(providers => setRegistrationEnabled(providers.registrationEnabled))
      .catch(setProviderError)
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !registrationEnabled || passwordBytes > 72 || passwordCodepoints < 12) return
    setBusy(true)
    setError(undefined)
    try {
      const response = await fetch(apiUrl('/auth/register', '/api'), {
        method: 'POST', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' },
        credentials: getCredentialsMode(), redirect: 'error', cache: 'no-store',
        body: JSON.stringify({ email: email.trim(), password }), signal: AbortSignal.timeout(10000),
      })
      const result: unknown = await response.json().catch(() => undefined)
      if (!response.ok) {
        const details = result && typeof result === 'object' ? result as Record<string, unknown> : {}
        const message = [details.error_code, details.message, details.requestId].filter((value): value is string => typeof value === 'string' && !!value.trim()).join(' · ')
        throw new Error(message || (response.status === 409 ? '该邮箱已注册，请使用现有账号登录。' : '注册请求失败（HTTP ' + response.status + '）。'))
      }
      if (response.status !== 201 || !result || typeof result !== 'object' || (result as Record<string, unknown>).status !== 'pending') throw new Error('注册响应格式无效，申请状态无法确认。')
      setSubmitted(true)
    } catch (cause) {
      setError(cause instanceof Error ? cause : new Error('注册申请未能确认，请稍后重试。'))
    } finally { setPassword(''); setBusy(false) }
  }

  return <div className="portal-auth-page">
    <header className="portal-auth-header"><a href="/" aria-label="KubeAtlas 门户首页"><Globe size={28} />KubeAtlas <span>集群门户</span></a><ThemeToggle /></header>
    <main className="portal-auth-main portal-auth-form-layout"><section className="portal-card p-6 sm:p-9">
      <p className="portal-mono text-xs text-accent-text">KubeAtlas Portal</p><h1 className="mt-2 text-2xl font-semibold">创建账号</h1><p className="mt-2 text-sm text-theme-text-secondary">注册后等待平台 Owner 审批。请勿将此状态理解为邮箱或身份已验证。</p>
      <UnencryptedHttpWarning />
      {loading ? <CloudLoading label="正在读取注册配置…" /> : providerError ? <CloudError error={providerError} /> : !registrationEnabled ? <p role="status" className="portal-card mt-5 p-4 text-sm">当前自托管 Hub 未开放公开注册。请联系平台 Owner。</p> : submitted ? <div role="status" className="portal-card mt-5 p-4"><strong>注册申请已提交。</strong><p className="mt-2 text-sm text-theme-text-secondary">此申请不会建立登录会话。请前往登录查看账号审批状态。</p><CloudLink href="/login" className="cloud-button mt-4">前往登录查看状态</CloudLink></div> : <form className="cloud-form mt-6" onSubmit={submit} aria-busy={busy}>
        <label>邮箱<input type="email" autoComplete="email" maxLength={254} required className="portal-control w-full" value={email} disabled={busy} onChange={event => setEmail(event.target.value)} /></label>
        <label>密码<input type="password" autoComplete="new-password" minLength={12} maxLength={72} required className="portal-control w-full" value={password} disabled={busy} onChange={event => setPassword(event.target.value)} /><small>至少 12 个 Unicode 字符；UTF-8 编码最多 72 字节。</small></label>
        {passwordBytes > 72 && <p role="alert">密码的 UTF-8 编码超过 72 字节，请缩短密码。</p>}
        {error !== undefined && <CloudError error={error} />}
        <button type="submit" className="btn-brand inline-flex items-center justify-center gap-2 px-4 py-3" disabled={busy || passwordCodepoints < 12 || passwordBytes > 72}>{busy ? <LoaderCircle className="animate-spin" size={16} /> : null}{busy ? '正在提交…' : '提交注册申请'}</button>
      </form>}
      <p className="mt-6 border-t border-theme-border pt-5 text-sm text-theme-text-secondary">已有账号？ <CloudLink href="/login" className="text-accent-text">登录</CloudLink></p>
    </section></main>
    <footer className="portal-auth-footer">KubeAtlas Portal</footer>
  </div>
}
