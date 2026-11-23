import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Eye, EyeOff, Globe, LoaderCircle } from 'lucide-react'
import { CloudLoading } from './CloudShared'
import { CloudRequestError, cloudRequest } from './cloudApi'
import { replacePortalLocation } from './portalNavigation'
import { ThemeToggle } from './ThemeToggle'

export function Setup() {
  const setup = useQuery({
    queryKey: ['portal-setup'],
    queryFn: ({ signal }) => cloudRequest<{ required: boolean }>('/setup', { signal }),
  })
  const required = setup.data?.required
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const errorMessage = useRef<HTMLParagraphElement>(null)

  useEffect(() => {
    if (required === false) replacePortalLocation('/login')
  }, [required])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy) return
    const form = event.currentTarget
    const data = new FormData(form)
    const code = String(data.get('code') ?? '').trim()
    const username = String(data.get('username') ?? '').trim()
    const password = String(data.get('password') ?? '')
    const passwordBytes = new TextEncoder().encode(password).length
    if (passwordBytes > 4096 || [...password].length < 8 || !/[A-Z]/.test(password) || !/[\p{P}\p{S}]/u.test(password)) {
      const input = form.elements.namedItem('password') as HTMLInputElement
      input.setCustomValidity(passwordBytes > 4096 ? '密码过长，请缩短后重试。' : '至少 8 个字符，含大写英文字母和符号。')
      input.reportValidity()
      return
    }

    setBusy(true)
    setError('')
    try {
      await cloudRequest<void>('/setup', { method: 'POST', body: { code, username, password } })
      replacePortalLocation('/login')
    } catch (failure) {
      const status = failure instanceof CloudRequestError ? failure.status : undefined
      if (status === 409) {
        replacePortalLocation('/login')
        return
      }
      const messages: Record<number, string> = {
        400: '请检查管理员账号和密码后重试。',
        403: '请核对初始化码，并通过 Hub 的访问地址重试。',
        429: '尝试过于频繁，请稍后重试。',
      }
      setError((status && messages[status]) || '暂时无法初始化，请稍后重试。')
      setBusy(false)
      requestAnimationFrame(() => errorMessage.current?.focus())
    }
  }

  return <div className="portal-auth-page">
    <header className="portal-auth-header">
      <a href="/" aria-label="KubeAtlas 门户首页"><Globe size={28} />KubeAtlas <span>集群门户</span></a>
      <ThemeToggle />
    </header>
    <main className="portal-auth-main portal-auth-form-layout portal-register-layout">
      <section className="portal-card p-6 sm:p-9" aria-labelledby="setup-title">
        <h1 id="setup-title">创建平台管理员</h1>
        <div className="mt-7">
          {setup.isPending || required === false ? <CloudLoading label={required === false ? '正在跳转至登录页…' : '正在检查初始化状态…'} /> : required !== true ? <div className="space-y-3">
            <p role="alert" className="text-sm text-theme-text-secondary">暂时无法读取初始化状态。</p>
            <button className="cloud-button" onClick={() => { void setup.refetch() }} disabled={setup.isFetching}>重试</button>
          </div> : <form className="cloud-form portal-register-form" method="post" onSubmit={submit} aria-busy={busy}>
            <div>
              <label htmlFor="setup-code">一次性初始化码</label>
              <input id="setup-code" name="code" autoComplete="one-time-code" autoCapitalize="none" spellCheck={false} className="portal-control w-full" required disabled={busy} aria-describedby="setup-code-hint" />
              <p id="setup-code-hint" className="portal-field-hint mt-2">请使用 Hub 安装时生成的初始化码。</p>
            </div>
            <div>
              <label htmlFor="setup-username">管理员账号</label>
              <input id="setup-username" name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} className="portal-control w-full" required maxLength={256} disabled={busy} />
            </div>
            <div>
              <label htmlFor="setup-password">管理员密码</label>
              <div className="portal-password-control">
                <input id="setup-password" name="password" type={showPassword ? 'text' : 'password'} autoComplete="new-password" autoCapitalize="none" spellCheck={false} className="portal-control w-full" required maxLength={4096} disabled={busy} aria-describedby="setup-password-hint" onInput={event => event.currentTarget.setCustomValidity('')} />
                <button type="button" aria-label="显示密码" aria-pressed={showPassword} disabled={busy} onClick={() => setShowPassword(value => !value)}>
                  {showPassword ? <EyeOff size={18} aria-hidden="true" /> : <Eye size={18} aria-hidden="true" />}
                </button>
              </div>
              <p id="setup-password-hint" className="portal-field-hint mt-2">至少 8 个字符，含大写英文字母和符号</p>
            </div>
            {error && <p ref={errorMessage} role="alert" tabIndex={-1} className="portal-field-error">{error}</p>}
            <button type="submit" className="btn-brand inline-flex items-center justify-center gap-2 px-4 py-3" disabled={busy}>
              {busy && <LoaderCircle className="animate-spin" size={16} aria-hidden="true" />}
              {busy ? '正在创建…' : '创建管理员'}
            </button>
          </form>}
          <p className="sr-only" role="status">{busy ? '正在创建平台管理员…' : ''}</p>
        </div>
      </section>
    </main>
  </div>
}
