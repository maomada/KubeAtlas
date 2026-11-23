// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, type QueryClient as QueryClientType } from '@tanstack/react-query'
import { beforeEach, expect, it, vi } from 'vitest'
import { PortalEntry } from './CloudPortal'
import { navigateCloud } from './cloudNavigation'
import { logoutPortal, type HubContext } from './CloudOrganization'
import { HUB_YAML_DRAFT_AUTHORITY_MARKER, yamlDraftStorageKey } from '@maomada/k8s-ui'
import { LocaleProvider, useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { LEGACY_YAML_DRAFT_CHANNEL } from '@maomada/k8s-ui/utils/legacy-brand'

const portalNavigation = vi.hoisted(() => ({
  replace: vi.fn(), nativeClient: undefined as unknown, nativeProps: undefined as unknown,
  beforeNativeRender: undefined as ((props: { yamlDraftScope?: string }) => void) | undefined,
  nativeMounts: 0, nativeUnmounts: 0,
}))

vi.mock('./portalNavigation', () => ({ replacePortalLocation: portalNavigation.replace }))

const scope = {
  actives: ['demo-shop'], accessibleNamespaces: ['demo-shop', 'demo-staging'],
  mode: 'namespace', cacheScoped: false, authoritative: true, canClearNamespace: true,
}
const connections = { lanURL: 'https://lan.example.test', publicURL: 'https://public.example.test', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: false, catalog: { available: false } }

const portalHub = {
  enabled: true, userId: 'test-user', authorizationRevision: '1',
  member: { id: 'member-1', userId: 'test-user', displayName: 'Test User', role: 'owner' as const, status: 'active' as const },
  platformOwner: false, activeOrganizationId: 'org-1',
  organizations: [{ id: 'org-1', name: 'Organization One', role: 'owner' as const }],
}

vi.mock('../KubeAtlasApp', async () => {
  const { useEffect, useState } = await import('react')
  return {
    KubeAtlasApp: (props: { queryClient: QueryClientType; apiBase?: string; yamlDraftScope?: string; yamlReadOnly?: boolean }) => {
      portalNavigation.beforeNativeRender?.(props)
      portalNavigation.nativeProps = props
      const [reviewOpen, setReviewOpen] = useState(false)
      useEffect(() => {
        portalNavigation.nativeMounts++
        return () => { portalNavigation.nativeUnmounts++ }
      }, [])
      return <>
        <button onClick={() => {
          const { queryClient } = props
          portalNavigation.nativeClient = queryClient
          queryClient.setQueryData(['namespace-scope'], { ...scope, actives: ['demo-staging'] })
        }}>Change native namespace</button>
        <button onClick={() => setReviewOpen(true)}>Open native review</button>
        {reviewOpen && <span data-testid="native-review">Review changes</span>}
      </>
    },
  }
})

vi.mock('@maomada/k8s-ui', async importOriginal => {
  const actual = await importOriginal<typeof import('@maomada/k8s-ui')>()
  return {
    ...actual,
    NamespacePicker: ({ onApply, scope }: { onApply: (namespaces: string[]) => void; scope?: { actives: string[] } }) =>
      <>
        <span>{scope?.actives.join(',')}</span>
        <button onClick={() => onApply(['demo-staging'])}>Apply host namespace</button>
      </>,
  }
})

async function waitForPortal(assertion: () => void, options?: Parameters<typeof vi.waitFor>[1]) {
  await vi.waitFor(async () => {
    await act(async () => {})
    assertion()
  }, options)
}

it('switches the portal language from preferences and restores it after a reload', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const languageKey = 'kubeatlas-portal-language'
  const previousLanguage = localStorage.getItem(languageKey)
  const previousDocumentLanguage = document.documentElement.lang
  const browserLanguages = vi.spyOn(window.navigator, 'languages', 'get').mockReturnValue(['zh-Hant', 'en-GB'])
  localStorage.removeItem(languageKey)
  window.history.replaceState({}, '', '/settings')
  vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected request: ' + path)
  }))
  const host = document.createElement('div')
  document.body.appendChild(host)
  let root = createRoot(host)
  const languageSelect = () => host.querySelector<HTMLSelectElement>('.cloud-preference select')!
  async function render() {
    await act(async () => root.render(<LocaleProvider><PortalEntry /></LocaleProvider>))
    await waitForPortal(() => expect(languageSelect()).not.toBeNull())
  }
  async function selectLanguage(value: string) {
    await act(async () => {
      languageSelect().value = value
      languageSelect().dispatchEvent(new Event('change', { bubbles: true }))
    })
  }
  try {
    await render()
    expect(languageSelect().value).toBe('zh-CN')
    expect(languageSelect().labels?.[0].textContent).toBe('语言')

    await selectLanguage('en')
    expect(languageSelect().labels?.[0].textContent).toBe('Language')
    expect(host.querySelector('.cloud-page-heading h1')?.textContent).toBe('Preferences')
    expect(host.querySelector('.cloud-sidebar nav a[href="/issues"]')?.textContent).toBe('Issues')
    expect(host.querySelector('a[href="/settings/organization/agent-access"]')?.textContent).toBe('MCP')
    expect(document.documentElement.lang).toBe('en')
    expect(localStorage.getItem(languageKey)).toBe('en')

    act(() => root.unmount())
    root = createRoot(host)
    await render()
    expect(languageSelect().value).toBe('en')

    await selectLanguage('zh-CN')
    expect(host.querySelector('.cloud-page-heading h1')?.textContent).toBe('偏好设置')
    expect(document.documentElement.lang).toBe('zh-CN')
  } finally {
    act(() => root.unmount())
    host.remove()
    browserLanguages.mockRestore()
    document.documentElement.lang = previousDocumentLanguage
    if (previousLanguage === null) localStorage.removeItem(languageKey)
    else localStorage.setItem(languageKey, previousLanguage)
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each([
  { label: 'the first supported preference', languages: ['fr-FR', 'en-GB', 'zh-CN'], language: 'fr-FR', stored: null, expected: 'en' },
  { label: 'a stored override', languages: ['en-US'], language: 'en-US', stored: 'zh-CN', expected: 'zh-CN' },
  { label: 'the single-language fallback', languages: [], language: 'en-AU', stored: null, expected: 'en' },
  { label: 'Chinese when no preference is supported', languages: ['fr-FR', 'ja-JP'], language: 'fr-FR', stored: 'invalid', expected: 'zh-CN' },
  { label: 'browser preference when storage is blocked', languages: ['EN-us'], language: 'EN-us', stored: null, expected: 'en', blocked: true },
])('uses $label on initial portal render', async ({ languages, language, stored, expected, blocked }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const languageKey = 'kubeatlas-portal-language'
  const storage = window.localStorage
  const previousLanguage = storage.getItem(languageKey)
  const previousDocumentLanguage = document.documentElement.lang
  if (stored === null) storage.removeItem(languageKey)
  else storage.setItem(languageKey, stored)
  const browserLanguages = vi.spyOn(window.navigator, 'languages', 'get').mockReturnValue(languages)
  const browserLanguage = vi.spyOn(window.navigator, 'language', 'get').mockReturnValue(language)
  const browserStorage = blocked ? vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => { throw new DOMException('Storage is blocked', 'SecurityError') }) : undefined
  const host = document.createElement('div')
  const root = createRoot(host)
  function LocaleProbe() {
    return <output>{useLocale().locale}</output>
  }
  try {
    await act(async () => root.render(<LocaleProvider><LocaleProbe /></LocaleProvider>))
    expect(host.querySelector('output')?.textContent).toBe(expected)
    expect(document.documentElement.lang).toBe(expected)
    expect(storage.getItem(languageKey)).toBe(stored)
  } finally {
    act(() => root.unmount())
    browserLanguages.mockRestore()
    browserLanguage.mockRestore()
    browserStorage?.mockRestore()
    if (previousLanguage === null) storage.removeItem(languageKey)
    else storage.setItem(languageKey, previousLanguage)
    document.documentElement.lang = previousDocumentLanguage
  }
})

beforeEach(() => {
  sessionStorage.clear()
  portalNavigation.replace.mockClear()
  portalNavigation.nativeClient = undefined
  portalNavigation.nativeProps = undefined
  portalNavigation.beforeNativeRender = undefined
  portalNavigation.nativeMounts = 0
  portalNavigation.nativeUnmounts = 0
})

it('translates an existing sign-in error without retrying the request', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const languageKey = 'kubeatlas-portal-language'
  const previousLanguage = localStorage.getItem(languageKey)
  const previousDocumentLanguage = document.documentElement.lang
  localStorage.setItem(languageKey, 'zh-CN')
  window.history.replaceState({}, '', '/login')
  const post = vi.fn(() => new Response(null, { status: 401 }))
  vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/auth/providers') return Response.json({ registrationEnabled: false })
    if (String(input) === '/api/auth/login') return post()
    throw new Error(`Unexpected request: ${input}`)
  }))
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<LocaleProvider><PortalEntry /></LocaleProvider>))
    const form = host.querySelector('form')!
    const languageSelect = host.querySelector<HTMLSelectElement>('header select')!
    await act(async () => {
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    expect(form.querySelector('[role="alert"]')?.textContent).toBe('账号或密码不正确，请重新输入。')
    await act(async () => {
      languageSelect.value = 'en'
      languageSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(form.querySelector('[role="alert"]')?.textContent).toBe('Incorrect username or password. Try again.')
    expect(post).toHaveBeenCalledTimes(1)
  } finally {
    act(() => root.unmount())
    host.remove()
    if (previousLanguage === null) localStorage.removeItem(languageKey)
    else localStorage.setItem(languageKey, previousLanguage)
    document.documentElement.lang = previousDocumentLanguage
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('validates and submits the first-run form before opening the login page', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const languageKey = 'kubeatlas-portal-language'
  const previousLanguage = localStorage.getItem(languageKey)
  const previousDocumentLanguage = document.documentElement.lang
  localStorage.setItem(languageKey, 'zh-CN')
  window.history.replaceState({}, '', '/setup')
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, options) => {
    if (String(input) !== '/api/setup') throw new Error(`Unexpected request: ${input}`)
    if (options?.method === 'GET') return Response.json({ required: true })
    const { code } = JSON.parse(String(options?.body))
    if (code === 'wrong-code') return Response.json({ message: 'Server error details' }, { status: 403 })
    return new Response(null, { status: 204 })
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<LocaleProvider><PortalEntry /></LocaleProvider>))
    await waitForPortal(() => expect(host.querySelector('form')).not.toBeNull())
    const form = host.querySelector('form')!
    const code = form.querySelector<HTMLInputElement>('[name="code"]')!
    const username = form.querySelector<HTMLInputElement>('[name="username"]')!
    const password = form.querySelector<HTMLInputElement>('[name="password"]')!
    code.value = 'wrong-code'
    username.value = 'platform-admin'
    for (const invalidPassword of ['管理测试用A🚀', '管理员测试用a！', '管理员测试用Aa']) {
      password.value = invalidPassword
      await act(async () => {
        password.dispatchEvent(new Event('input', { bubbles: true }))
        expect(password.validationMessage).toBe('')
        form.requestSubmit()
      })
      expect(password.validationMessage).toBe('至少 8 个字符，含大写英文字母和符号。')
      expect(fetch.mock.calls).toHaveLength(1)
    }

    const languageSelect = host.querySelector<HTMLSelectElement>('header select')!
    await act(async () => {
      languageSelect.value = 'en'
      languageSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(password.validationMessage).toBe('')
    await act(async () => form.requestSubmit())
    expect(password.validationMessage).toBe('At least 8 characters, including an uppercase English letter and a symbol.')
    expect(fetch.mock.calls).toHaveLength(1)

    const validPassword = '管理员测试用A！'
    password.value = validPassword
    await act(async () => {
      password.dispatchEvent(new Event('input', { bubbles: true }))
      form.requestSubmit()
    })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Check the setup code')
    expect(portalNavigation.replace).not.toHaveBeenCalled()
    const attempts = fetch.mock.calls.length
    await act(async () => {
      languageSelect.value = 'zh-CN'
      languageSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('请核对初始化码')
    expect(fetch.mock.calls).toHaveLength(attempts)

    code.value = 'valid-code'
    await act(async () => form.requestSubmit())
    expect(fetch).toHaveBeenLastCalledWith('/api/setup', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ code: 'valid-code', username: 'platform-admin', password: validPassword }),
    }))
    expect(portalNavigation.replace).toHaveBeenCalledWith('/login')
  } finally {
    act(() => root.unmount())
    host.remove()
    if (previousLanguage === null) localStorage.removeItem(languageKey)
    else localStorage.setItem(languageKey, previousLanguage)
    document.documentElement.lang = previousDocumentLanguage
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each([
  { label: 'missing Hub', firstResponse: () => Promise.resolve(new Response(null, { status: 404 })) },
  { label: 'disabled Hub', firstResponse: () => Promise.resolve(Response.json({ enabled: false })) },
  { label: 'network failure', firstResponse: () => Promise.reject(new Error('network interruption')) },
  { label: 'permission denial', firstResponse: () => Promise.resolve(new Response(null, { status: 403 })) },
  { label: 'source failure', firstResponse: () => Promise.resolve(new Response(null, { status: 503 })) },
  { label: 'invalid metadata', firstResponse: () => Promise.resolve(Response.json({ enabled: 'true' })) },
  { label: 'missing authorization revision', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, authorizationRevision: undefined })) },
  { label: 'empty authorization revision', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, authorizationRevision: '' })) },
  { label: 'noncanonical authorization revision', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, authorizationRevision: '01' })) },
  { label: 'line-terminated authorization revision', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, authorizationRevision: '1\n' })) },
  { label: 'out-of-range authorization revision', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, authorizationRevision: '18446744073709551616' })) },
  { label: 'missing user identity', firstResponse: () => Promise.resolve(Response.json({ ...portalHub, userId: '' })) },
])('fails closed on $label and retries without opening the retired Portal', async ({ firstResponse }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings')
  let availabilityRequests = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') {
      availabilityRequests++
      return availabilityRequests === 1 ? firstResponse() : Response.json(portalHub)
    }
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('[role="alert"]')).not.toBeNull())
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/clusters' || String(url).startsWith('/api/fleet'))).toBe(false)
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '重试')!.click())
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    expect(host.querySelector('a[href="/fleet"]')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/clusters' || String(url).startsWith('/api/fleet'))).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each([
  { id: 'alpha', eligible: false },
  { id: 'beta', eligible: true },
])('keeps the investigation route with explicit eligibility feedback ($id)', async ({ id, eligible }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', `/c/${id}/investigations`)
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: ['alpha', 'beta'].map(id => ({ id, organizationId: 'org-1', name: id === 'alpha' ? 'Alpha' : 'Beta', state: 'connected' })) })
    if (path === `/c/${id}/api/agents`) return Response.json({ eligible, enabled: false, agents: [] })
    if (path === `/c/${id}/api/cluster/namespace-scope`) return Response.json(scope)
    if (path === `/c/${id}/api/api-resources`) return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  }))
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    if (eligible) {
      await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    } else {
      await waitForPortal(() => expect(host.textContent).toContain('AI 调查功能不可用'))
      expect(host.textContent).not.toContain('Change native namespace')
    }
    expect(window.location.pathname).toBe(`/c/${id}/investigations`)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('reserves host search shortcuts before native listeners without stealing editor keys', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings?cluster=alpha')
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  }))
  const scrollIntoView = Element.prototype.scrollIntoView
  Element.prototype.scrollIntoView = vi.fn()
  const host = document.createElement('div')
  const nativeInput = document.createElement('input')
  document.body.append(host, nativeInput)
  const nativeShortcut = vi.fn((event: KeyboardEvent) => {
    if (event.key === '/' || event.key === 'k') setTimeout(() => nativeInput.focus(), 0)
  })
  document.addEventListener('keydown', nativeShortcut)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('Alpha'))
    const search = host.querySelector('header input')!
    for (const keys of [{ key: '/' }, { key: 'k', ctrlKey: true }, { key: 'k', metaKey: true }]) {
      const event = new KeyboardEvent('keydown', { ...keys, bubbles: true, cancelable: true })
      await act(async () => {
        document.body.dispatchEvent(event)
        await new Promise(resolve => setTimeout(resolve, 0))
      })
      expect(event.defaultPrevented).toBe(true)
      expect(nativeShortcut).not.toHaveBeenCalled()
      expect(document.activeElement).toBe(search)
      act(() => (search as HTMLElement).blur())
    }
    act(() => nativeInput.focus())
    const typedSlash = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true })
    await act(async () => {
      nativeInput.dispatchEvent(typedSlash)
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    expect(typedSlash.defaultPrevented).toBe(false)
    expect(document.activeElement).toBe(nativeInput)
    for (const editor of ['monaco-editor', 'xterm']) {
      nativeInput.className = editor
      const event = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, cancelable: true })
      await act(async () => {
        nativeInput.dispatchEvent(event)
        await new Promise(resolve => setTimeout(resolve, 0))
      })
      expect(event.defaultPrevented).toBe(false)
      expect(document.activeElement).toBe(nativeInput)
    }
  } finally {
    document.removeEventListener('keydown', nativeShortcut)
    act(() => root.unmount())
    host.remove(); nativeInput.remove()
    Element.prototype.scrollIntoView = scrollIntoView
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('shares the native namespace cache when entering and switching clusters', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings?cluster=alpha')
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const responses: Record<string, unknown> = {
      '/api/hub': portalHub,
      '/api/hub/connections': connections,
      '/api/hub/clusters': { clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }, { id: 'beta', organizationId: 'org-1', name: 'Beta', state: 'connected' }] },
      '/c/alpha/api/cluster/namespace-scope': scope,
      '/c/alpha/api/api-resources': [],
      '/c/beta/api/cluster/namespace-scope': scope,
      '/c/beta/api/api-resources': [],
    }
    if (String(input) === '/api/auth/session') return new Response(null, { status: 204 })
    if (!(String(input) in responses)) throw new Error(`Unexpected request: ${input}`)
    return new Response(JSON.stringify(responses[String(input)]), { headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('Alpha'))
    await act(async () => navigateCloud('/c/alpha/resources/pods?namespaces=demo-shop'))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    const topology = [...host.querySelectorAll('a')].find(link => link.textContent === '拓扑')
    expect(topology?.getAttribute('href')).toBe('/c/alpha/topology?namespaces=demo-shop')
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop'))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging'))
    expect(host.querySelector('header')?.textContent).not.toContain('demo-shop')
    await act(async () => navigateCloud('/c/beta/topology?namespaces=demo-shop'))
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop'))
    expect(host.querySelector('header')?.textContent).toContain('Beta')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging'))
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('reconciles the namespace cache after an uncertain host write', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/pods?namespaces=demo-shop')
  const updatedScope = { ...scope, actives: ['demo-staging'] }
  let scopeReads = 0
  let apiResourceReads = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/cluster/namespace-scope') {
      scopeReads++
      return Response.json(scopeReads === 1 ? scope : updatedScope)
    }
    if (path === '/c/alpha/api/api-resources') {
      apiResourceReads++
      return Response.json([])
    }
    if (path === '/c/alpha/api/cluster/namespace') {
      expect(init?.method).toBe('POST')
      expect(init?.body).toBe(JSON.stringify({ namespaces: ['demo-staging'] }))
      return Response.json(
        { error_code: 'source_unavailable', message: 'Source response lost', outcome: 'unknown' },
        { status: 502 },
      )
    }
    throw new Error(`Unexpected request: ${input}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop'))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Apply host namespace')!.click())
    await waitForPortal(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging'))
    expect(scopeReads).toBe(2)
    expect(apiResourceReads).toBe(1)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('probes the root session without redirecting a 401 and shows public links', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 401 })
    if (path === '/api/auth/providers') return Response.json({ registrationEnabled: true, oidc: { enabled: false } })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('KubeAtlas · 自托管集群门户'))
    expect(host.querySelector('a[href="/login"]')).not.toBeNull()
    expect(host.querySelector('a[href="/register"]')).not.toBeNull()
    expect(window.location.pathname).toBe('/')
    const probe = fetch.mock.calls.find(([url]) => String(url) === '/api/auth/session')
    expect(probe?.[1]).toMatchObject({ redirect: 'error', cache: 'no-store' })
    expect(fetch.mock.calls.some(([url]) => ['/api/hub', '/api/hub/connections', '/api/hub/clusters', '/api/mcp/credentials', '/api/clusters'].includes(String(url)))).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('redirects a private unauthenticated route to the login page', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings')
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/auth/session') return new Response(null, { status: 401 })
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  const replace = portalNavigation.replace
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(replace).toHaveBeenCalledWith('/login'))
    expect(fetch.mock.calls.some(([url]) => ['/api/hub', '/api/hub/connections', '/api/hub/clusters', '/api/mcp/credentials'].includes(String(url)))).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('shows a retry for a non-401 session failure without mounting Portal resources', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  let sessionReads = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return ++sessionReads === 1 ? new Response(null, { status: 503 }) : new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('[role="alert"]')?.textContent).toContain('HTTP 503'))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub')).toBe(false)
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '重试')!.click())
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    expect(sessionReads).toBe(2)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each([
  { label: 'pending', memberStatus: 'pending', organizations: [{ id: 'org-1', name: 'Org', role: 'member' }], activeOrganizationId: 'org-1', expected: '等待平台 Owner 审批' },
  { label: 'active without a role', memberStatus: 'active', organizations: [{ id: 'org-1', name: 'Org', role: 'member' }], activeOrganizationId: 'org-1', expected: '门户返回的数据无效' },
  { label: 'disabled', memberStatus: 'disabled', organizations: [{ id: 'org-1', name: 'Org', role: 'member' }], activeOrganizationId: 'org-1', expected: '账号已停用' },
  { label: 'zero membership', memberStatus: 'active', organizations: [], activeOrganizationId: null, expected: '暂无组织成员资格' },
])('does not mount resource providers for $label accounts', async ({ memberStatus, organizations, activeOrganizationId, expected }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const member = memberStatus === 'pending'
    ? { id: 'member-1', userId: 'test-user', displayName: 'Test User', status: memberStatus }
    : { ...portalHub.member, status: memberStatus }
  const fixture = { ...portalHub, member, organizations, activeOrganizationId, platformOwner: false }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') {
      if (expected === '门户返回的数据无效') return Response.json({ ...fixture, member: { ...fixture.member, role: undefined } })
      return Response.json(fixture)
    }
    throw new Error('Unexpected gated request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain(expected))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => ['/api/hub/connections', '/api/hub/clusters', '/api/mcp/credentials', '/api/clusters'].includes(String(url)))).toBe(false)
    if (memberStatus === 'pending') {
      expect(host.textContent).toContain('不代表身份或邮箱已验证')
      expect(host.textContent).not.toMatch(/email (?:has been )?verified/i)
    }
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('clears both query clients and reloads after the exact organization-select request', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const hub = { ...portalHub, organizations: [...portalHub.organizations, { id: 'org-2', name: 'Organization Two', role: 'member' as const }] }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    if (path === '/api/hub/organization/select') return new Response(null, { status: 204 })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const clear = vi.spyOn(QueryClient.prototype, 'clear')
  const cancel = vi.spyOn(QueryClient.prototype, 'cancelQueries')
  const replace = portalNavigation.replace
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    const select = host.querySelector<HTMLSelectElement>('#portal-organization-switcher')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'org-2')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).toBeNull())
    const request = fetch.mock.calls.find(([url]) => String(url) === '/api/hub/organization/select')
    expect(request?.[1]?.method).toBe('POST')
    expect(JSON.parse(request?.[1]?.body as string)).toEqual({ organizationId: 'org-2' })
    expect(replace).toHaveBeenCalledWith('/')
    expect(cancel.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(clear.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(window.localStorage.getItem('activeOrganizationId')).toBeNull()
  } finally {
    act(() => root.unmount())
    host.remove()
    clear.mockRestore()
    cancel.mockRestore()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('keeps the current organization and reports a failed switch without reloading', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const hub = { ...portalHub, organizations: [...portalHub.organizations, { id: 'org-2', name: 'Organization Two', role: 'member' as const }] }
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    if (path === '/api/hub/organization/select' && init?.method === 'POST') return Response.json({ message: 'switch rejected' }, { status: 503 })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const replace = portalNavigation.replace
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    const select = host.querySelector<HTMLSelectElement>('#portal-organization-switcher')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'org-2')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await waitForPortal(() => expect(host.textContent).toContain('switch rejected'))
    expect(host.querySelector('.cloud-shell')).not.toBeNull()
    expect(host.querySelector<HTMLSelectElement>('#portal-organization-switcher')?.value).toBe('org-1')
    expect(replace).not.toHaveBeenCalled()
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

const authorizationTransitions = [
  {
    label: 'a cross-tab organization switch',
    nextHub: {
      ...portalHub,
      authorizationRevision: '2',
      member: { ...portalHub.member, role: 'member' as const },
      activeOrganizationId: 'org-2',
      organizations: [...portalHub.organizations, { id: 'org-2', name: 'Organization Two', role: 'member' as const }],
    },
  },
  {
    label: 'a same-organization owner-to-viewer demotion',
    nextHub: {
      ...portalHub,
      authorizationRevision: '2',
      member: { ...portalHub.member, role: 'viewer' as const },
      organizations: [{ ...portalHub.organizations[0], role: 'viewer' as const }],
    },
  },
  {
    label: 'same-value authority ABA with a higher revision',
    nextHub: { ...portalHub, authorizationRevision: '3' },
  },
]

it.each(authorizationTransitions)('clears old caches after $label, but not for an unchanged authorization poll', async ({ nextHub }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources')
  const oldOrganizationCredential = {
    id: 'credential-org-1', organizationId: 'org-1', label: 'Old organization credential',
    clusterIds: ['alpha'], readOnly: true, createdAt: '2026-01-01T00:00:00Z',
    expiresAt: '2030-01-01T00:00:00Z', refreshUntil: '2030-02-01T00:00:00Z',
  }
  const hubResponses = [portalHub, portalHub, nextHub]
  let hubReads = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hubResponses[Math.min(hubReads++, hubResponses.length - 1)])
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Old organization cluster', state: 'connected' }] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const queryClients = new Set<QueryClientType>()
  const originalGetQueryCache = QueryClient.prototype.getQueryCache
  const queryCacheSpy = vi.spyOn(QueryClient.prototype, 'getQueryCache').mockImplementation(function (this: QueryClientType) {
    queryClients.add(this)
    return originalGetQueryCache.call(this)
  })
  const clear = vi.spyOn(QueryClient.prototype, 'clear')
  const cancel = vi.spyOn(QueryClient.prototype, 'cancelQueries')
  const replace = portalNavigation.replace
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    await waitForPortal(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change native namespace')).toBe(true))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    const nativeClient = portalNavigation.nativeClient as QueryClientType
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeDefined()

    let portalClient: QueryClientType | undefined
    const mcpQueryKey = ['portal', '/api', '/mcp/credentials'] as const
    await waitForPortal(() => {
      portalClient = [...queryClients].find(client => client.getQueryData(['portal', '/api', '/hub']) !== undefined)
      expect(portalClient).toBeDefined()
    })
    act(() => { portalClient!.setQueryData(mcpQueryKey, { credentials: [oldOrganizationCredential], serverTime: '2026-01-01T00:00:00Z' }) })
    expect((portalClient!.getQueryData(mcpQueryKey) as { credentials: { organizationId: string }[] }).credentials[0]?.organizationId).toBe('org-1')

    await act(async () => { await portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] }) })
    await waitForPortal(() => expect(hubReads).toBe(2))
    expect(replace).not.toHaveBeenCalled()
    expect(clear).not.toHaveBeenCalled()
    expect(portalClient!.getQueryData(mcpQueryKey)).toBeDefined()
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeDefined()

    await act(async () => {
      await portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] })
    })
    await waitForPortal(() => expect(replace).toHaveBeenCalledWith('/'))

    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(host.textContent).not.toContain('Old organization credential')
    expect(host.textContent).not.toContain('Old organization cluster')
    expect(portalClient!.getQueryData(mcpQueryKey)).toBeUndefined()
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeUndefined()
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/clusters')).toHaveLength(1)
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/mcp/credentials')).toHaveLength(0)
    expect(clear.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(cancel.mock.calls.length).toBeGreaterThanOrEqual(2)
  } finally {
    act(() => root.unmount())
    host.remove()
    clear.mockRestore()
    cancel.mockRestore()
    queryCacheSpy.mockRestore()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

const platformLocations = [
  { label: 'canonical', initial: '/platform?source=bookmark#accounts', legacy: false },
  { label: 'legacy', initial: '/settings/platform?source=bookmark#accounts', legacy: true },
]

it.each(platformLocations)('keeps $label platform endpoints inaccessible to an organization owner named admin', async ({ initial }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', initial)
  const hub = { ...portalHub, userId: 'admin', member: { ...portalHub.member, userId: 'admin', displayName: 'admin' } }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    throw new Error('Unauthorized platform request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    await waitForPortal(() => expect(host.querySelector('[role="alert"]')?.textContent).toContain('platformOwner'))
    expect(window.location.pathname + window.location.search + window.location.hash).toBe('/platform?source=bookmark#accounts')
    expect(host.querySelector('.cloud-settings')).toBeNull()
    expect(host.querySelector('a[aria-label="平台管理"]')).toBeNull()
    expect(host.querySelector('a[aria-label="设置"][aria-current="page"]')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url).includes('/hub/platform/') || String(url) === '/api/hub/organizations')).toBe(false)
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/mcp/credentials')).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each(platformLocations)('allows a $label platform owner route without an active organization', async ({ initial, legacy }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', initial)
  const replaceHistory = vi.spyOn(window.history, 'replaceState')
  const pushHistory = vi.spyOn(window.history, 'pushState')
  const hub = { enabled: true, userId: 'platform-user', authorizationRevision: '1', platformOwner: true, activeOrganizationId: null, organizations: [] }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/platform/users') return Response.json({ users: [] })
    if (path === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (path === '/api/hub/platform/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('main h1')?.textContent).toBe('平台管理'))
    await waitForPortal(() => expect(fetch.mock.calls.filter(([url]) => ['/api/hub/platform/users', '/api/hub/organizations', '/api/hub/platform/clusters'].includes(String(url)))).toHaveLength(3))
    expect(window.location.pathname + window.location.search + window.location.hash).toBe('/platform?source=bookmark#accounts')
    expect(replaceHistory.mock.calls.filter(([, , url]) => String(url) === '/platform?source=bookmark#accounts')).toHaveLength(legacy ? 1 : 0)
    expect(pushHistory).not.toHaveBeenCalled()
    expect(portalNavigation.replace).not.toHaveBeenCalled()
    expect(host.querySelector('.cloud-platform-only')).not.toBeNull()
    expect(host.querySelector('.cloud-settings')).toBeNull()
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => ['/api/hub/connections', '/api/hub/clusters', '/api/mcp/credentials'].includes(String(url)))).toBe(false)
  } finally {
    act(() => root.unmount())
    replaceHistory.mockRestore()
    pushHistory.mockRestore()
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it.each(platformLocations)('keeps $label platform administration outside settings for an active-organization admin', async ({ initial, legacy }) => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', initial)
  const replaceHistory = vi.spyOn(window.history, 'replaceState')
  const pushHistory = vi.spyOn(window.history, 'pushState')
  const hub = { ...portalHub, platformOwner: true }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    if (path === '/api/hub/platform/users') return Response.json({ users: [] })
    if (path === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (path === '/api/hub/platform/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected platform route request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('main h1')?.textContent).toBe('平台管理'))
    await waitForPortal(() => expect(fetch.mock.calls.filter(([url]) => ['/api/hub/platform/users', '/api/hub/organizations', '/api/hub/platform/clusters'].includes(String(url)))).toHaveLength(3))
    expect(window.location.pathname + window.location.search + window.location.hash).toBe('/platform?source=bookmark#accounts')
    expect(replaceHistory.mock.calls.filter(([, , url]) => String(url) === '/platform?source=bookmark#accounts')).toHaveLength(legacy ? 1 : 0)
    expect(pushHistory).not.toHaveBeenCalled()
    expect(portalNavigation.replace).not.toHaveBeenCalled()
    expect(host.querySelector('.cloud-shell')).not.toBeNull()
    expect(host.querySelector('.cloud-settings')).toBeNull()
    expect(host.querySelector('a[aria-label="平台管理"]')?.getAttribute('href')).toBe('/platform')
    expect(host.querySelector('a[aria-label="平台管理"]')?.getAttribute('aria-current')).toBe('page')
    expect(host.querySelector('a[aria-label="设置"]')?.hasAttribute('aria-current')).toBe(false)
    expect(host.querySelector('a[href="/settings/platform"]')).toBeNull()
    expect(host.querySelector('.cloud-setup')).toBeNull()
    expect(host.textContent).not.toContain('入门设置')
    expect(host.textContent).not.toContain('设置自托管 Hub')
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/mcp/credentials')).toBe(false)
  } finally {
    act(() => root.unmount())
    replaceHistory.mockRestore()
    pushHistory.mockRestore()
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('opens canonical platform administration from organization selection without mounting organization resources', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const hub = { enabled: true, userId: 'platform-user', authorizationRevision: '1', platformOwner: true, activeOrganizationId: null, organizations: [] }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/platform/users') return Response.json({ users: [] })
    if (path === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (path === '/api/hub/platform/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected no-organization request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('a[href="/platform"]')?.textContent).toBe('平台管理'))
    expect(fetch.mock.calls.some(([url]) => String(url).includes('/hub/platform/') || String(url) === '/api/hub/organizations')).toBe(false)
    await act(async () => host.querySelector<HTMLAnchorElement>('a[href="/platform"]')!.click())
    await waitForPortal(() => expect(host.querySelector('main h1')?.textContent).toBe('平台管理'))
    expect(window.location.pathname).toBe('/platform')
    expect(host.querySelector('.cloud-platform-only')).not.toBeNull()
    expect(host.querySelector('.cloud-settings')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => ['/api/hub/connections', '/api/hub/clusters', '/api/mcp/credentials'].includes(String(url)))).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('mounts an active-organization shell for an explicit platform owner without an org member record', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/')
  const hub = { ...portalHub, member: null, platformOwner: true }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.querySelector('.cloud-shell')).not.toBeNull())
    expect(host.querySelector('a[aria-label="平台管理"]')?.getAttribute('href')).toBe('/platform')
    await act(async () => {
      window.history.replaceState({}, '', '/settings')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    await waitForPortal(() => expect(host.querySelector('.cloud-settings')).not.toBeNull())
    expect([...host.querySelectorAll('.cloud-settings > aside h2')].map(heading => heading.textContent)).toEqual(['个人', '组织'])
    expect(host.querySelector('.cloud-settings a[href="/platform"], .cloud-settings a[href="/settings/platform"]')).toBeNull()
    expect(host.querySelector('a[aria-label="平台管理"]')?.getAttribute('href')).toBe('/platform')
    expect(host.querySelector('a[aria-label="设置"]')?.getAttribute('aria-current')).toBe('page')
    expect(host.querySelector('.cloud-setup')).toBeNull()
    expect(host.textContent).not.toContain('入门设置')
    expect(host.textContent).not.toContain('设置自托管 Hub')
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/mcp/credentials')).toBe(false)
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub/connections')).toBe(true)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('keeps a native review mounted while an unchanged Hub poll is pending and after it resolves', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources')
  let hubReads = 0
  let finishHubPoll: (response: Response) => void = () => {}
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') {
      hubReads++
      if (hubReads === 1) return Response.json(portalHub)
      return new Promise<Response>(resolve => { finishHubPoll = resolve })
    }
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const queryClients = new Set<QueryClientType>()
  const originalGetQueryCache = QueryClient.prototype.getQueryCache
  const queryCacheSpy = vi.spyOn(QueryClient.prototype, 'getQueryCache').mockImplementation(function (this: QueryClientType) {
    queryClients.add(this)
    return originalGetQueryCache.call(this)
  })
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).not.toBeNull())
    await waitForPortal(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Open native review')).toBe(true))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open native review')!.click())
    expect(host.querySelector('[data-testid="native-review"]')?.textContent).toBe('Review changes')
    expect(portalNavigation.nativeMounts).toBe(1)

    const portalClient = [...queryClients].find(client => client.getQueryData(['portal', '/api', '/hub']) !== undefined)
    expect(portalClient).toBeDefined()
    let refetch: Promise<void> | undefined
    await act(async () => {
      refetch = portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] })
      await Promise.resolve()
    })
    await waitForPortal(() => expect(hubReads).toBe(2))
    expect(portalNavigation.nativeMounts).toBe(1)
    expect(portalNavigation.nativeUnmounts).toBe(0)
    expect(host.querySelector('[data-testid="native-review"]')?.textContent).toBe('Review changes')

    await act(async () => {
      finishHubPoll(Response.json(portalHub))
      await refetch
    })
    expect(portalNavigation.nativeMounts).toBe(1)
    expect(portalNavigation.nativeUnmounts).toBe(0)
    expect(host.querySelector('[data-testid="native-review"]')?.textContent).toBe('Review changes')
  } finally {
    act(() => root.unmount())
    queryCacheSpy.mockRestore()
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('purges the prior YAML draft scope before native mount after a fresh login at revision N+2', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  let hub: HubContext = { ...portalHub, authorizationRevision: '1' }
  let authenticated = true
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: authenticated ? 204 : 401 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const queryClients = new Set<QueryClientType>()
  const originalGetQueryCache = QueryClient.prototype.getQueryCache
  const queryCacheSpy = vi.spyOn(QueryClient.prototype, 'getQueryCache').mockImplementation(function (this: QueryClientType) {
    queryClients.add(this)
    return originalGetQueryCache.call(this)
  })
  const host = document.createElement('div')
  document.body.appendChild(host)
  let root = createRoot(host)
  let rootMounted = true
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    const initialProps = portalNavigation.nativeProps as { yamlDraftScope?: string }
    const resource = { group: '', kind: 'ConfigMap', namespace: 'qa', name: 'revision-aba' }
    const oldScope = initialProps.yamlDraftScope!
    const oldDraftKey = yamlDraftStorageKey(oldScope, resource)
    sessionStorage.setItem(oldDraftKey, 'old-authority-draft')
    sessionStorage.setItem('unrelated:portal-setting', 'preserve-session-storage')
    localStorage.setItem('unrelated:portal-setting', 'preserve-local-storage')
    const oldMarker = sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)
    let portalClient: QueryClientType | undefined
    await waitForPortal(() => {
      portalClient = [...queryClients].find(client => client.getQueryData(['portal', '/api', '/hub']) !== undefined)
      expect(portalClient).toBeDefined()
    })
    authenticated = false
    await act(async () => { await portalClient!.invalidateQueries({ queryKey: ['portal-session'] }) })
    await waitForPortal(() => expect(portalNavigation.replace).toHaveBeenCalledWith('/login'))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(sessionStorage.getItem(oldDraftKey)).toBe('old-authority-draft')

    act(() => root.unmount())
    rootMounted = false
    authenticated = true
    hub = { ...portalHub, authorizationRevision: '3' }
    portalNavigation.nativeProps = undefined
    let checkedBeforeNativeMount = false
    portalNavigation.beforeNativeRender = props => {
      expect(sessionStorage.getItem(oldDraftKey)).toBeNull()
      expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).not.toBe(oldMarker)
      expect(props.yamlDraftScope).toBeDefined()
      expect(props.yamlDraftScope).not.toBe(oldScope)
      expect(sessionStorage.getItem(yamlDraftStorageKey(props.yamlDraftScope!, resource))).toBeNull()
      expect(sessionStorage.getItem('unrelated:portal-setting')).toBe('preserve-session-storage')
      expect(localStorage.getItem('unrelated:portal-setting')).toBe('preserve-local-storage')
      checkedBeforeNativeMount = true
    }
    root = createRoot(host)
    rootMounted = true
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    expect(checkedBeforeNativeMount).toBe(true)
    expect(portalNavigation.nativeMounts).toBe(2)
    expect(portalNavigation.nativeUnmounts).toBe(1)
    expect(sessionStorage.getItem(oldDraftKey)).toBeNull()
    expect(sessionStorage.getItem('unrelated:portal-setting')).toBe('preserve-session-storage')
    expect(localStorage.getItem('unrelated:portal-setting')).toBe('preserve-local-storage')
  } finally {
    portalNavigation.beforeNativeRender = undefined
    if (rootMounted) act(() => root.unmount())
    queryCacheSpy.mockRestore()
    host.remove()
    localStorage.removeItem('unrelated:portal-setting')
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('retains same-revision YAML drafts through a session 401 and a fresh Portal mount', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  let authenticated = true
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: authenticated ? 204 : 401 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const queryClients = new Set<QueryClientType>()
  const originalGetQueryCache = QueryClient.prototype.getQueryCache
  const queryCacheSpy = vi.spyOn(QueryClient.prototype, 'getQueryCache').mockImplementation(function (this: QueryClientType) {
    queryClients.add(this)
    return originalGetQueryCache.call(this)
  })
  const host = document.createElement('div')
  document.body.appendChild(host)
  let root = createRoot(host)
  let rootMounted = true
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    const initialProps = portalNavigation.nativeProps as { yamlDraftScope?: string }
    const draftKey = yamlDraftStorageKey(initialProps.yamlDraftScope!, {
      group: '', kind: 'ConfigMap', namespace: 'qa', name: 'same-revision',
    })
    sessionStorage.setItem(draftKey, 'same-authority-draft')
    const marker = sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)
    let portalClient: QueryClientType | undefined
    await waitForPortal(() => {
      portalClient = [...queryClients].find(client => client.getQueryData(['portal', '/api', '/hub']) !== undefined)
      expect(portalClient).toBeDefined()
    })

    authenticated = false
    await act(async () => { await portalClient!.invalidateQueries({ queryKey: ['portal-session'] }) })
    await waitForPortal(() => expect(portalNavigation.replace).toHaveBeenCalledWith('/login'))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(sessionStorage.getItem(draftKey)).toBe('same-authority-draft')

    act(() => root.unmount())
    rootMounted = false
    authenticated = true
    portalNavigation.nativeProps = undefined
    root = createRoot(host)
    rootMounted = true
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toBe(marker)
    expect(sessionStorage.getItem(draftKey)).toBe('same-authority-draft')
    expect(portalNavigation.nativeMounts).toBe(2)
  } finally {
    if (rootMounted) act(() => root.unmount())
    queryCacheSpy.mockRestore()
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('unmounts native editing before local logout purges drafts and stays gated after logout failure', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  sessionStorage.clear()
  let draftKey: string | undefined
  let finishLogout: (response: Response) => void = () => {}
  let logoutStartedAfterUnmount = false
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    if (path === '/api/auth/logout') {
      logoutStartedAfterUnmount = portalNavigation.nativeUnmounts === 1 && draftKey !== undefined && sessionStorage.getItem(draftKey) === null
      return new Promise<Response>(resolve => { finishLogout = resolve })
    }
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Open native review')).toBe(true))
    const nativeProps = portalNavigation.nativeProps as { yamlDraftScope: string }
    draftKey = yamlDraftStorageKey(nativeProps.yamlDraftScope, { group: '', kind: 'ConfigMap', namespace: 'qa', name: 'config' })
    sessionStorage.setItem(draftKey, 'owner-unsaved')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open native review')!.click())
    expect(host.querySelector('[data-testid="native-review"]')).not.toBeNull()

    let logout: Promise<void> | undefined
    await act(async () => {
      logout = logoutPortal().catch(() => {})
      await Promise.resolve()
    })
    expect(logoutStartedAfterUnmount).toBe(true)
    expect(sessionStorage.getItem(draftKey)).toBeNull()
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toBeNull()
    expect(host.querySelector('[data-testid="native-review"]')).toBeNull()

    finishLogout(Response.json({ message: 'Logout unavailable' }, { status: 503 }))
    await act(async () => { await logout })
    expect(host.textContent).toContain('Logout unavailable')
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(sessionStorage.getItem(draftKey)).toBeNull()
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('attempts server logout when local draft removal fails and stays gated', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  sessionStorage.clear()
  let draftKey: string | undefined
  let nativeUnmountsWhenLogoutRequested = 0
  let draftWhenLogoutRequested: string | null = null
  let removeItemAttempts = 0
  let removeItemAttemptsWhenLogoutRequested = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    if (path === '/api/auth/logout') {
      nativeUnmountsWhenLogoutRequested = portalNavigation.nativeUnmounts
      draftWhenLogoutRequested = draftKey === undefined ? null : sessionStorage.getItem(draftKey)
      removeItemAttemptsWhenLogoutRequested = removeItemAttempts
      return Response.json({ logoutUrl: '/login' })
    }
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
  let logoutError: unknown
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Open native review')).toBe(true))
    const nativeProps = portalNavigation.nativeProps as { yamlDraftScope: string }
    draftKey = yamlDraftStorageKey(nativeProps.yamlDraftScope, { group: '', kind: 'ConfigMap', namespace: 'qa', name: 'config' })
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open native review')!.click())
    expect(host.querySelector('[data-testid="native-review"]')).not.toBeNull()
    sessionStorage.setItem(draftKey, 'owner-unsaved')
    expect(sessionStorage.getItem(draftKey)).toBe('owner-unsaved')
    removeItem.mockImplementation(() => {
      removeItemAttempts++
      throw new Error('Storage access denied')
    })

    await act(async () => {
      try {
        await logoutPortal()
      } catch (error) {
        logoutError = error
      }
    })

    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/auth/logout')).toHaveLength(1)
    expect(nativeUnmountsWhenLogoutRequested).toBe(1)
    expect(removeItemAttemptsWhenLogoutRequested).toBeGreaterThan(0)
    expect(draftWhenLogoutRequested).toBe('owner-unsaved')
    expect(removeItem).toHaveBeenCalled()
    expect(portalNavigation.nativeMounts).toBe(1)
    expect(portalNavigation.nativeUnmounts).toBe(1)
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(host.querySelector('[data-testid="native-review"]')).toBeNull()
    expect(host.textContent).toContain('服务器已完成退出登录')
    expect(host.textContent).toContain('未能删除已保存的 YAML 草稿')
    expect(portalNavigation.replace).not.toHaveBeenCalled()
    expect(sessionStorage.getItem(draftKey)).toBe('owner-unsaved')
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).not.toBeNull()
    expect(logoutError).toBeInstanceOf(Error)
    expect((logoutError as Error).message).toContain('未能删除已保存的 YAML 草稿')
  } finally {
    removeItem.mockRestore()
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('keeps Portal gated and reports remote YAML storage failure honestly', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  sessionStorage.clear()
  let draftKey: string | undefined
  let nativeUnmountsWhenClearStarted = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
  let remoteSender: BroadcastChannel | undefined
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Open native review')).toBe(true))
    const nativeProps = portalNavigation.nativeProps as { yamlDraftScope: string }
    draftKey = yamlDraftStorageKey(nativeProps.yamlDraftScope, { group: '', kind: 'ConfigMap', namespace: 'qa', name: 'config' })
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open native review')!.click())
    expect(host.querySelector('[data-testid="native-review"]')).not.toBeNull()
    sessionStorage.setItem(draftKey, 'remote-unsaved')
    removeItem.mockImplementation(() => {
      nativeUnmountsWhenClearStarted = portalNavigation.nativeUnmounts
      throw new Error('Storage access denied')
    })
    remoteSender = new BroadcastChannel(LEGACY_YAML_DRAFT_CHANNEL)

    await act(async () => {
      remoteSender?.postMessage({ type: 'purge' })
      await new Promise(resolve => setTimeout(resolve, 10))
    })
    await waitForPortal(() => expect(host.textContent).toContain('Unable to remove saved YAML drafts after a session change in another tab'))

    expect(nativeUnmountsWhenClearStarted).toBe(1)
    expect(portalNavigation.nativeMounts).toBe(1)
    expect(portalNavigation.nativeUnmounts).toBe(1)
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(host.querySelector('[data-testid="native-review"]')).toBeNull()
    expect(host.textContent).not.toContain('saved YAML drafts were discarded')
    expect(sessionStorage.getItem(draftKey)).toBe('remote-unsaved')
    expect(removeItem).toHaveBeenCalled()
  } finally {
    removeItem.mockRestore()
    remoteSender?.close()
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('wires scoped read-only YAML policy and purges drafts on a mounted same-org role change', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
  sessionStorage.clear()
  sessionStorage.setItem('kubeatlas_yaml_draft:ConfigMap/qa-cdf2599-native/kubeatlas-agent-88ca3f0', 'legacy-owner-draft')
  let hub: HubContext = portalHub
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(hub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', organizationId: 'org-1', name: 'Alpha', state: 'connected' }] })
    if (path === '/c/alpha/api/agents') return Response.json({ eligible: true, enabled: false, agents: [] })
    if (path === '/c/alpha/api/cluster/namespace-scope') return Response.json(scope)
    if (path === '/c/alpha/api/api-resources') return Response.json([])
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const queryClients = new Set<QueryClientType>()
  const originalGetQueryCache = QueryClient.prototype.getQueryCache
  const queryCacheSpy = vi.spyOn(QueryClient.prototype, 'getQueryCache').mockImplementation(function (this: QueryClientType) {
    queryClients.add(this)
    return originalGetQueryCache.call(this)
  })
  const host = document.createElement('div')
  document.body.appendChild(host)
  let root = createRoot(host)
  let rootMounted = true
  try {
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    let portalClient: QueryClientType | undefined
    await waitForPortal(() => {
      portalClient = [...queryClients].find(client => client.getQueryData(['portal', '/api', '/hub']) !== undefined)
      expect(portalClient).toBeDefined()
    })
    const ownerProps = portalNavigation.nativeProps as { apiBase?: string; yamlDraftScope?: string; yamlReadOnly?: boolean }
    expect(ownerProps.apiBase).toBe('/c/alpha/api')
    expect(ownerProps.yamlReadOnly).toBe(false)
    expect(ownerProps.yamlDraftScope).toContain('"clusterId":"alpha"')
    expect(ownerProps.yamlDraftScope).toContain('/c/alpha/api')
    expect(sessionStorage.getItem('kubeatlas_yaml_draft:ConfigMap/qa-cdf2599-native/kubeatlas-agent-88ca3f0')).toBeNull()
    expect(sessionStorage.getItem('activeOrganizationId')).toBeNull()
    const oldDraftKey = yamlDraftStorageKey(ownerProps.yamlDraftScope!, {
      group: '', kind: 'ConfigMap', namespace: 'qa-cdf2599-native', name: 'kubeatlas-agent-88ca3f0',
    })
    sessionStorage.setItem(oldDraftKey, 'owner-unsaved')

    hub = {
      ...portalHub,
      authorizationRevision: '2',
      member: { ...portalHub.member!, role: 'viewer' as const },
      organizations: [{ ...portalHub.organizations[0], role: 'viewer' as const }],
    }
    portalNavigation.nativeProps = undefined
    await act(async () => { await portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] }) })
    await waitForPortal(() => expect(portalNavigation.replace).toHaveBeenCalledWith('/'))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(portalNavigation.nativeUnmounts).toBe(1)
    expect(portalNavigation.nativeProps).toBeUndefined()
    expect(sessionStorage.getItem(oldDraftKey)).toBeNull()
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toContain('viewer')

    act(() => root.unmount())
    rootMounted = false
    window.history.replaceState({}, '', '/c/alpha/resources/configmaps')
    portalNavigation.nativeProps = undefined
    root = createRoot(host)
    rootMounted = true
    await act(async () => root.render(<PortalEntry />))
    await waitForPortal(() => expect(host.textContent).toContain('Change native namespace'))
    const viewerProps = portalNavigation.nativeProps as { yamlDraftScope?: string; yamlReadOnly?: boolean }
    expect(viewerProps.yamlReadOnly).toBe(true)
    expect(viewerProps.yamlDraftScope).not.toBe(ownerProps.yamlDraftScope)
    expect(sessionStorage.getItem(oldDraftKey)).toBeNull()
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toContain('viewer')
    expect(portalNavigation.nativeMounts).toBe(2)
    expect(portalNavigation.nativeUnmounts).toBe(1)
  } finally {
    if (rootMounted) act(() => root.unmount())
    queryCacheSpy.mockRestore()
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})
