// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, type QueryClient as QueryClientType } from '@tanstack/react-query'
import { beforeEach, expect, it, vi } from 'vitest'
import { PortalEntry } from './CloudPortal'
import { navigateCloud } from './cloudNavigation'

const portalNavigation = vi.hoisted(() => ({ replace: vi.fn(), nativeClient: undefined as unknown }))

vi.mock('./portalNavigation', () => ({ replacePortalLocation: portalNavigation.replace }))

const scope = {
  actives: ['demo-shop'], accessibleNamespaces: ['demo-shop', 'demo-staging'],
  mode: 'namespace', cacheScoped: false, authoritative: true, canClearNamespace: true,
}
const connections = { lanURL: 'https://lan.example.test', publicURL: 'https://public.example.test', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: false, catalog: { available: false } }

const portalHub = {
  enabled: true, userId: 'test-user',
  member: { id: 'member-1', userId: 'test-user', displayName: 'Test User', role: 'owner' as const, status: 'active' as const },
  platformOwner: false, activeOrganizationId: 'org-1',
  organizations: [{ id: 'org-1', name: 'Organization One', role: 'owner' as const }],
}

vi.mock('../RadarApp', () => ({
  RadarApp: ({ queryClient }: { queryClient: QueryClientType }) => <button onClick={() => {
    portalNavigation.nativeClient = queryClient
    queryClient.setQueryData(['namespace-scope'], { ...scope, actives: ['demo-staging'] })
  }}>Change native namespace</button>,
}))

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

beforeEach(() => {
  portalNavigation.replace.mockClear()
  portalNavigation.nativeClient = undefined
})

it.each([
  { label: 'missing Hub', firstResponse: () => Promise.resolve(new Response(null, { status: 404 })) },
  { label: 'disabled Hub', firstResponse: () => Promise.resolve(Response.json({ enabled: false })) },
  { label: 'network failure', firstResponse: () => Promise.reject(new Error('network interruption')) },
  { label: 'permission denial', firstResponse: () => Promise.resolve(new Response(null, { status: 403 })) },
  { label: 'source failure', firstResponse: () => Promise.resolve(new Response(null, { status: 503 })) },
  { label: 'invalid metadata', firstResponse: () => Promise.resolve(Response.json({ enabled: 'true' })) },
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.querySelector('[role="alert"]')).not.toBeNull()))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/clusters' || String(url).startsWith('/api/fleet'))).toBe(false)
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '重试')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain(eligible ? 'Opening live cluster…' : 'AI investigations unavailable')))
    expect(window.location.pathname).toBe(`/c/${id}/investigations`)
    if (!eligible) expect(host.textContent).not.toContain('Change native namespace')
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('Alpha')))
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
      '/api/mcp/credentials': { credentials: [], serverTime: '2026-01-01T00:00:00Z' },
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('Alpha')))
    await act(async () => navigateCloud('/c/alpha/resources/pods?namespaces=demo-shop'))
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Change native namespace')))
    const topology = [...host.querySelectorAll('a')].find(link => link.textContent === 'Topology')
    expect(topology?.getAttribute('href')).toBe('/c/alpha/topology?namespaces=demo-shop')
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop')))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging')))
    expect(host.querySelector('header')?.textContent).not.toContain('demo-shop')
    await act(async () => navigateCloud('/c/beta/topology?namespaces=demo-shop'))
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop')))
    expect(host.querySelector('header')?.textContent).toContain('Beta')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging')))
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop')))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Apply host namespace')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging')))
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
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('KubeAtlas · 自托管集群门户')))
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
    await act(async () => vi.waitFor(() => expect(replace).toHaveBeenCalledWith('/login')))
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.querySelector('[role="alert"]')?.textContent).toContain('HTTP 503')))
    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub')).toBe(false)
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '重试')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
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
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain(expected)))
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
    const select = host.querySelector<HTMLSelectElement>('#portal-organization-switcher')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'org-2')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).toBeNull()))
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
    const select = host.querySelector<HTMLSelectElement>('#portal-organization-switcher')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'org-2')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('switch rejected')))
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

it('clears old caches when Hub reports a same-organization role change, but not for an unchanged authorization poll', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources')
  const nextHub = {
    ...portalHub,
    member: { ...portalHub.member, role: 'viewer' as const },
    organizations: [{ ...portalHub.organizations[0], role: 'viewer' as const }],
  }
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [oldOrganizationCredential], serverTime: '2026-01-01T00:00:00Z' })
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
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
    await act(async () => vi.waitFor(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Change native namespace')).toBe(true)))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    const nativeClient = portalNavigation.nativeClient as QueryClientType
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeDefined()

    let portalClient: QueryClientType | undefined
    const mcpQueryKey = ['portal', '/api', '/mcp/credentials'] as const
    await act(async () => vi.waitFor(() => {
      portalClient = [...queryClients].find(client => client.getQueryData(mcpQueryKey) !== undefined && client.getQueryData(['portal', '/api', '/hub']) !== undefined)
      expect(portalClient).toBeDefined()
    }))
    expect((portalClient!.getQueryData(mcpQueryKey) as { credentials: { organizationId: string }[] }).credentials[0]?.organizationId).toBe('org-1')

    await act(async () => { await portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] }) })
    await act(async () => vi.waitFor(() => expect(hubReads).toBe(2)))
    expect(replace).not.toHaveBeenCalled()
    expect(clear).not.toHaveBeenCalled()
    expect(portalClient!.getQueryData(mcpQueryKey)).toBeDefined()
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeDefined()

    await act(async () => {
      await portalClient!.invalidateQueries({ queryKey: ['portal', '/api', '/hub'] })
    })
    await act(async () => vi.waitFor(() => expect(replace).toHaveBeenCalledWith('/')))

    expect(host.querySelector('.cloud-shell')).toBeNull()
    expect(host.textContent).not.toContain('Old organization credential')
    expect(host.textContent).not.toContain('Old organization cluster')
    expect(portalClient!.getQueryData(mcpQueryKey)).toBeUndefined()
    expect(nativeClient.getQueryData(['namespace-scope'])).toBeUndefined()
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/clusters')).toHaveLength(1)
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/mcp/credentials')).toHaveLength(1)
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

it('keeps platform endpoints inaccessible to an organization owner', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings/platform')
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const path = String(input)
    if (path === '/api/auth/session') return new Response(null, { status: 204 })
    if (path === '/api/hub') return Response.json(portalHub)
    if (path === '/api/hub/connections') return Response.json(connections)
    if (path === '/api/hub/clusters') return Response.json({ clusters: [] })
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    throw new Error('Unauthorized platform request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
    expect(host.textContent).toContain('平台管理需要明确的平台 Owner 权限')
    expect(fetch.mock.calls.some(([url]) => String(url).includes('/hub/platform/') || String(url) === '/api/hub/organizations')).toBe(false)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('allows an explicit platform owner to manage users without an active organization', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/settings/platform')
  const hub = { enabled: true, userId: 'platform-user', platformOwner: true, activeOrganizationId: null, organizations: [] }
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
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('平台管理')))
    expect(fetch.mock.calls.some(([url]) => ['/api/hub/platform/users', '/api/hub/organizations', '/api/hub/platform/clusters'].includes(String(url)))).toBe(true)
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
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-shell')).not.toBeNull()))
    expect(host.querySelector('a[aria-label="平台管理"]')).not.toBeNull()
    await act(async () => {
      window.history.replaceState({}, '', '/settings')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-settings a[href="/settings/platform"]')?.textContent).toBe('平台管理')))
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub/connections')).toBe(true)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})
