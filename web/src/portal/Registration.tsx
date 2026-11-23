import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { ArrowRight, CheckCircle2, Eye, EyeOff, Globe, LoaderCircle, ShieldCheck } from 'lucide-react'
import { useLocale, type Translate } from '@maomada/k8s-ui/hooks/useLocale'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'
import { CloudError, CloudLoading, CloudLink } from './CloudShared'
import { AuthProvidersError, fetchAuthProviders } from './auth'
import { PortalHeaderActions } from './PortalLanguageSelect'

export function PortalLanding() {
  const { t } = useLocale()
  useEffect(() => { document.title = t('集群门户 · KubeAtlas', 'Cluster portal · KubeAtlas') }, [t])
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
    <header className="portal-auth-header"><a href="/" aria-label={t('KubeAtlas 门户首页', 'KubeAtlas portal home')}><Globe size={28} />KubeAtlas <span className="hidden sm:inline">{t('集群门户', 'Cluster portal')}</span></a><PortalHeaderActions /></header>
    <main className="portal-auth-main">
      <section><p className="portal-mono text-xs text-accent-text">{t('KubeAtlas · 自托管集群门户', 'KubeAtlas · Self-hosted cluster portal')}</p><h1>{t('从一个入口，管理多个组织与集群', 'Manage organizations and clusters in one place')}</h1><p className="text-theme-text-secondary">{t('查看已授权的集群状态，按组织管理访问，并通过显式配对流程连接新的集群。', 'View clusters you can access, manage organization access, and pair new clusters.')}</p><div className="mt-7 flex flex-wrap gap-3"><CloudLink href="/login" className="btn-brand inline-flex items-center gap-2 px-5 py-3">{t('登录', 'Sign in')} <ArrowRight size={16} /></CloudLink>{loading ? <span role="status">{t('正在读取注册配置…', 'Checking registration availability…')}</span> : registrationEnabled ? <CloudLink href="/register" className="cloud-button">{t('创建账号', 'Create account')}</CloudLink> : <span className="text-sm text-theme-text-tertiary">{t('账号注册当前不可用', 'Registration is currently unavailable')}</span>}</div>
        <div className="mt-8 flex items-start gap-3"><ShieldCheck size={19} className="mt-0.5 shrink-0 text-accent-text" /><p className="text-sm text-theme-text-secondary">{t('每个组织的 Hub 权限独立于 Kubernetes RBAC；只有平台所有者可以管理平台用户与组织。', 'Hub permissions are managed per organization alongside Kubernetes RBAC. Platform owners manage platform users and organizations.')}</p></div>
      </section>
      <section className="portal-card p-6"><h2 className="text-xl font-semibold">{t('按组织管理集群', 'Manage clusters by organization')}</h2><p className="mt-3 text-sm text-theme-text-secondary">{t('与团队共享集群，按成员角色分配访问权限。', 'Share clusters with your team and assign access by role.')}</p></section>
    </main>
    <footer className="portal-auth-footer">{t('KubeAtlas Portal · 独立部署，集中访问', 'KubeAtlas Portal · Self-hosted cluster access')}</footer>
  </div>
}

type RegistrationField = 'email' | 'password'

const validationMessages = {
  emailRequired: ['请输入邮箱。', 'Enter your email address.'],
  emailInvalid: ['请输入有效的邮箱地址。', 'Enter a valid email address.'],
  passwordShort: ['密码至少需要 6 个字符。', 'Use at least 6 characters.'],
  passwordLong: ['密码过长，请缩短后重试。', 'Password is too long. Use a shorter password.'],
} as const

type ValidationError = keyof typeof validationMessages

function validationError(field: RegistrationField, input: HTMLInputElement): ValidationError | undefined {
  if (field === 'email') {
    if (!input.value.trim()) return 'emailRequired'
    return input.validity.typeMismatch ? 'emailInvalid' : undefined
  }
  if (Array.from(input.value).length < 6) return 'passwordShort'
  return new TextEncoder().encode(input.value).length > 72 ? 'passwordLong' : undefined
}

function validationMessage(error: ValidationError | undefined, t: Translate): string {
  if (!error) return ''
  const [zh, en] = validationMessages[error]
  return t(zh, en)
}

const registrationErrorMessages = {
  email_exists: ['该邮箱已注册，请直接登录。', 'This email is already registered. Sign in instead.'],
  rate_limited: ['提交过于频繁，请稍后重试。', 'Too many attempts. Try again later.'],
  invalid_parameters: ['请检查邮箱和密码后重试。', 'Check your email and password, then try again.'],
  registration_closed: ['暂未开放注册，请联系管理员。', 'Registration is closed. Contact your administrator.'],
  capacity_exceeded: ['注册名额已满，请联系管理员。', 'Registration has reached capacity. Contact your administrator.'],
  unavailable: ['暂时无法注册，请稍后重试。', 'Unable to register. Try again later.'],
  invalidResponse: ['注册响应格式无效，申请状态无法确认。', 'The portal returned an invalid registration response. Your application status is unknown.'],
  unconfirmed: ['注册申请未能确认，请稍后重试。', 'Unable to confirm your registration. Try again later.'],
} as const

type RegistrationError = keyof typeof registrationErrorMessages

export function Register() {
  const { t } = useLocale()
  useEffect(() => { document.title = t('创建账号 · KubeAtlas', 'Create account · KubeAtlas') }, [t])
  const [registrationEnabled, setRegistrationEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [providerError, setProviderError] = useState<AuthProvidersError['reason']>()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<RegistrationError>()
  const [submitted, setSubmitted] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<RegistrationField, ValidationError>>>({})
  const [announcement, setAnnouncement] = useState<ValidationError>()
  const emailInput = useRef<HTMLInputElement>(null)
  const passwordInput = useRef<HTMLInputElement>(null)
  const edited = useRef(new Set<RegistrationField>())
  const id = useId()
  useEffect(() => {
    const controller = new AbortController()
    fetchAuthProviders(controller.signal)
      .then(providers => { if (!controller.signal.aborted) setRegistrationEnabled(providers.registrationEnabled) })
      .catch(cause => {
        if (!controller.signal.aborted) setProviderError(cause instanceof AuthProvidersError ? cause.reason : 'unavailable')
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])

  const providerErrorMessage = providerError === 'invalid-response'
    ? t('认证选项无效。', 'The portal returned invalid sign-in options.')
    : t('认证选项不可用。', 'Sign-in options are unavailable.')
  const submissionErrorMessage = error ? t(registrationErrorMessages[error][0], registrationErrorMessages[error][1]) : ''
  const emailErrorMessage = validationMessage(fieldErrors.email, t)
  const passwordErrorMessage = validationMessage(fieldErrors.password, t)
  useEffect(() => { emailInput.current?.setCustomValidity(emailErrorMessage) }, [emailErrorMessage])
  useEffect(() => { passwordInput.current?.setCustomValidity(passwordErrorMessage) }, [passwordErrorMessage])

  function updateField(field: RegistrationField, input: HTMLInputElement) {
    edited.current.add(field)
    if (field === 'email') setEmail(input.value)
    else setPassword(input.value)
    input.setCustomValidity('')
    if (fieldErrors[field] && !validationError(field, input)) {
      setFieldErrors(current => ({ ...current, [field]: undefined }))
      setAnnouncement(undefined)
    }
  }

  function validateOnBlur(field: RegistrationField, input: HTMLInputElement) {
    if (!edited.current.has(field)) return
    const error = validationError(field, input)
    input.setCustomValidity(validationMessage(error, t))
    setFieldErrors(current => ({ ...current, [field]: error }))
    setAnnouncement(error)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !registrationEnabled) return
    setAnnouncement(undefined)
    const emailError = validationError('email', emailInput.current!)
    const passwordError = validationError('password', passwordInput.current!)
    setFieldErrors({ email: emailError, password: passwordError })
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
        const code = details.error_code
        setError(typeof code === 'string' && Object.hasOwn(registrationErrorMessages, code) ? code as RegistrationError : 'unavailable')
        return
      }
      if (response.status !== 201 || !result || typeof result !== 'object' || (result as Record<string, unknown>).status !== 'pending') {
        setError('invalidResponse')
        return
      }
      setSubmitted(true)
    } catch {
      setError('unconfirmed')
    } finally { setPassword(''); setShowPassword(false); setBusy(false) }
  }

  return <div className="portal-auth-page">
    <header className="portal-auth-header"><a href="/" aria-label={t('KubeAtlas 门户首页', 'KubeAtlas portal home')}><Globe size={28} />KubeAtlas <span className="hidden sm:inline">{t('集群门户', 'Cluster portal')}</span></a><PortalHeaderActions /></header>
    <main className="portal-auth-main portal-auth-form-layout portal-register-layout"><section className="portal-card p-6 sm:p-9">
      <h1>{t('创建账号', 'Create account')}</h1><p className="mt-3 text-sm text-theme-text-secondary">{t('加入团队，开始管理集群。', 'Join your team and start managing clusters.')}</p>
      {loading ? <CloudLoading label={t('正在读取注册配置…', 'Checking registration availability…')} /> : providerError ? <CloudError error={new Error(providerErrorMessage)} /> : !registrationEnabled ? <p role="status" className="mt-5 text-sm">{t('暂未开放注册，请联系管理员。', 'Registration is closed. Contact your administrator.')}</p> : submitted ? <div role="status" className="portal-registration-success"><CheckCircle2 size={36} className="text-accent-text" /><h2>{t('注册申请已提交', 'Registration submitted')}</h2><p>{t('管理员审批后即可使用。', 'You can sign in once an administrator approves your account.')}</p><CloudLink href="/login" className="btn-brand mt-5 inline-flex items-center justify-center gap-2 px-5 py-3">{t('前往登录', 'Go to sign-in')}<ArrowRight size={16} /></CloudLink></div> : <form className="cloud-form portal-register-form mt-7" onSubmit={submit} noValidate aria-busy={busy}>
        <div><label htmlFor={`${id}-email`}>{t('邮箱', 'Email')}</label><input ref={emailInput} id={`${id}-email`} name="email" type="email" autoComplete="email" autoCapitalize="none" spellCheck={false} maxLength={254} required className="portal-control w-full" value={email} disabled={busy} aria-invalid={Boolean(fieldErrors.email)} aria-describedby={fieldErrors.email ? `${id}-email-error` : undefined} onChange={event => updateField('email', event.target)} onBlur={event => validateOnBlur('email', event.target)} />{fieldErrors.email && <p id={`${id}-email-error`} className="portal-field-error">{emailErrorMessage}</p>}</div>
        <div><label htmlFor={`${id}-password`}>{t('密码', 'Password')}<span id={`${id}-password-hint`} className="portal-field-hint">{t('至少 6 个字符', 'At least 6 characters')}</span></label><div className="portal-password-control"><input ref={passwordInput} id={`${id}-password`} name="password" type={showPassword ? 'text' : 'password'} autoComplete="new-password" minLength={6} pattern=".{6,}" required className="portal-control w-full" value={password} disabled={busy} aria-invalid={Boolean(fieldErrors.password)} aria-describedby={`${id}-password-hint${fieldErrors.password ? ` ${id}-password-error` : ''}`} onChange={event => updateField('password', event.target)} onBlur={event => validateOnBlur('password', event.target)} /><button type="button" aria-label={showPassword ? t('隐藏密码', 'Hide password') : t('显示密码', 'Show password')} aria-pressed={showPassword} disabled={busy} onClick={() => setShowPassword(value => !value)}>{showPassword ? <EyeOff size={18} /> : <Eye size={18} />}</button></div>{fieldErrors.password && <p id={`${id}-password-error`} className="portal-field-error">{passwordErrorMessage}</p>}</div>
        <p className="sr-only" aria-live="polite">{validationMessage(announcement, t)}</p>
        {error && <CloudError error={new Error(submissionErrorMessage)} />}
        <button type="submit" className="btn-brand inline-flex items-center justify-center gap-2 px-4 py-3" disabled={busy}>{busy ? <LoaderCircle className="animate-spin" size={16} /> : null}{busy ? t('正在提交…', 'Submitting…') : t('创建账号', 'Create account')}</button>
      </form>}
      <p className="mt-6 border-t border-theme-border pt-5 text-sm text-theme-text-secondary">{t('已有账号？', 'Already have an account?')} <CloudLink href="/login" className="text-accent-text">{t('登录', 'Sign in')}</CloudLink></p>
    </section></main>
    <footer className="portal-auth-footer">KubeAtlas Portal</footer>
  </div>
}
