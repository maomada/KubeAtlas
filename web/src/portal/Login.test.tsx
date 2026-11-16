import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Login } from './Login'
import { Register } from './Registration'

afterEach(() => vi.unstubAllGlobals())

describe('Portal login page', () => {
  it('renders a separate credential form without fetching or displaying the directory', () => {
    vi.stubGlobal('window', { location: { search: '' } })
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    const html = renderToStaticMarkup(<Login />)
    expect(html).toContain('登录集群门户')
    expect(html).toContain('autoComplete="username"')
    expect(html).toContain('autoComplete="current-password"')
    expect(html).toContain('type="password"')
    expect(html).not.toContain('搜索集群')
    expect(html).not.toContain('登录会话已过期')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('explains an expired session on the login page', () => {
    vi.stubGlobal('window', { location: { search: '?expired=1' } })
    expect(renderToStaticMarkup(<Login />)).toContain('登录会话已过期，请重新登录。')
  })

  it('warns on non-loopback HTTP on login and registration pages', () => {
    vi.stubGlobal('window', { location: { search: '', href: 'http://portal.example.test/login' } })
    const login = renderToStaticMarkup(<Login />)
    expect(login).toContain('密码、会话 Cookie 和 Token 可能被观察或篡改')
    expect(login).toContain('HTTP + 字面 IP 仅在服务器明确配置并启用相应支持后')
    expect(login).not.toContain('--insecure-cookie')
    expect(renderToStaticMarkup(<Register />)).toContain('下载内容也可能被拦截或修改')
  })
})
