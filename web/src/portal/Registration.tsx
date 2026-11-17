import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { ArrowRight, CheckCircle2, Eye, EyeOff, Globe, LoaderCircle, ShieldCheck } from 'lucide-react'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { CloudError, CloudLoading, CloudLink } from './CloudShared'
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
      <section className="portal-card p-6"><h2 className="text-xl font-semibold">按组织管理集群</h2><p className="mt-3 text-sm text-theme-text-secondary">与团队共享集群，按成员角色分配访问权限。</p></section>
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
  const [showPassword, setShowPassword] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<{ email?: string; password?: string }>({})
  const [announcement, setAnnouncement] = useState('')
  const emailInput = useRef<HTMLInputElement>(null)
  const passwordInput = useRef<HTMLInputElement>(null)
  const edited = useRef(new Set<string>())
  const id = useId()
  useEffect(() => {
    const controller = new AbortController()
    fetchAuthProviders(controller.signal)
      .then(providers => { if (!controller.signal.aborted) setRegistrationEnabled(providers.registrationEnabled) })
      .catch(cause => { if (!controller.signal.aborted) setProviderError(cause) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])

  function validationMessage(field: 'email' | 'password', input: HTMLInputElement) {
    if (field === 'email') {
      if (!input.value.trim()) return '请输入邮箱。'
      return input.validity.typeMismatch ? '请输入有效的邮箱地址。' : ''
    }
    if (Array.from(input.value).length < 6) return '密码至少需要 6 个字符。'
    return new TextEncoder().encode(input.value).length > 72 ? '密码过长，请缩短后重试。' : ''
  }

  function updateField(field: 'email' | 'password', input: HTMLInputElement) {
    edited.current.add(field)
    if (field === 'email') setEmail(input.value)
    else setPassword(input.value)
    input.setCustomValidity('')
    if (fieldErrors[field] && !validationMessage(field, input)) {
      setFieldErrors(current => ({ ...current, [field]: undefined }))
      setAnnouncement('')
    }
  }

  function validateOnBlur(field: 'email' | 'password', input: HTMLInputElement) {
    if (!edited.current.has(field)) return
    const message = validationMessage(field, input)
    input.setCustomValidity(message)
    setFieldErrors(current => ({ ...current, [field]: message || undefined }))
    setAnnouncement(message)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !registrationEnabled) return
    setAnnouncement('')
    const emailError = validationMessage('email', emailInput.current!)
    const passwordError = validationMessage('password', passwordInput.current!)
    setFieldErrors({ email: emailError || undefined, password: passwordError || undefined })
    if (emailError || passwordError) {
      const input = emailError ? emailInput.current! : passwordInput.current!
      requestAnimationFrame(() => input.focus())
      return
    }
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
        const messages: Record<string, string> = {
          email_exists: '该邮箱已注册，请直接登录。',
          rate_limited: '提交过于频繁，请稍后重试。',
          invalid_parameters: '请检查邮箱和密码后重试。',
          registration_closed: '暂未开放注册，请联系管理员。',
          capacity_exceeded: '注册名额已满，请联系管理员。',
        }
        const code = typeof details.error_code === 'string' ? details.error_code : ''
        throw new Error(messages[code] || '暂时无法注册，请稍后重试。')
      }
      if (response.status !== 201 || !result || typeof result !== 'object' || (result as Record<string, unknown>).status !== 'pending') throw new Error('注册响应格式无效，申请状态无法确认。')
      setSubmitted(true)
    } catch (cause) {
      setError(cause instanceof Error ? cause : new Error('注册申请未能确认，请稍后重试。'))
    } finally { setPassword(''); setShowPassword(false); setBusy(false) }
  }

  return <div className="portal-auth-page">
    <header className="portal-auth-header"><a href="/" aria-label="KubeAtlas 门户首页"><Globe size={28} />KubeAtlas <span>集群门户</span></a><ThemeToggle /></header>
    <main className="portal-auth-main portal-auth-form-layout portal-register-layout"><section className="portal-card p-6 sm:p-9">
      <h1>创建账号</h1><p className="mt-3 text-sm text-theme-text-secondary">加入团队，开始管理集群。</p>
      {loading ? <CloudLoading label="正在读取注册配置…" /> : providerError ? <CloudError error={providerError} /> : !registrationEnabled ? <p role="status" className="mt-5 text-sm">暂未开放注册，请联系管理员。</p> : submitted ? <div role="status" className="portal-registration-success"><CheckCircle2 size={36} className="text-accent-text" /><h2>注册申请已提交</h2><p>管理员审批后即可使用。</p><CloudLink href="/login" className="btn-brand mt-5 inline-flex items-center justify-center gap-2 px-5 py-3">前往登录<ArrowRight size={16} /></CloudLink></div> : <form className="cloud-form portal-register-form mt-7" onSubmit={submit} noValidate aria-busy={busy}>
        <div><label htmlFor={`${id}-email`}>邮箱</label><input ref={emailInput} id={`${id}-email`} name="email" type="email" autoComplete="email" autoCapitalize="none" spellCheck={false} maxLength={254} required className="portal-control w-full" value={email} disabled={busy} aria-invalid={Boolean(fieldErrors.email)} aria-describedby={fieldErrors.email ? `${id}-email-error` : undefined} onChange={event => updateField('email', event.target)} onBlur={event => validateOnBlur('email', event.target)} />{fieldErrors.email && <p id={`${id}-email-error`} className="portal-field-error">{fieldErrors.email}</p>}</div>
        <div><label htmlFor={`${id}-password`}>密码<span id={`${id}-password-hint`} className="portal-field-hint">至少 6 个字符</span></label><div className="portal-password-control"><input ref={passwordInput} id={`${id}-password`} name="password" type={showPassword ? 'text' : 'password'} autoComplete="new-password" minLength={6} pattern=".{6,}" required className="portal-control w-full" value={password} disabled={busy} aria-invalid={Boolean(fieldErrors.password)} aria-describedby={`${id}-password-hint${fieldErrors.password ? ` ${id}-password-error` : ''}`} onChange={event => updateField('password', event.target)} onBlur={event => validateOnBlur('password', event.target)} /><button type="button" aria-label={showPassword ? '隐藏密码' : '显示密码'} aria-pressed={showPassword} disabled={busy} onClick={() => setShowPassword(value => !value)}>{showPassword ? <EyeOff size={18} /> : <Eye size={18} />}</button></div>{fieldErrors.password && <p id={`${id}-password-error`} className="portal-field-error">{fieldErrors.password}</p>}</div>
        <p className="sr-only" aria-live="polite">{announcement}</p>
        {error !== undefined && <CloudError error={error} />}
        <button type="submit" className="btn-brand inline-flex items-center justify-center gap-2 px-4 py-3" disabled={busy}>{busy ? <LoaderCircle className="animate-spin" size={16} /> : null}{busy ? '正在提交…' : '创建账号'}</button>
      </form>}
      <p className="mt-6 border-t border-theme-border pt-5 text-sm text-theme-text-secondary">已有账号？ <CloudLink href="/login" className="text-accent-text">登录</CloudLink></p>
    </section></main>
    <footer className="portal-auth-footer">KubeAtlas Portal</footer>
  </div>
}
