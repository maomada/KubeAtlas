// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudMembers, CloudSSO, PendingMembership, type HubMember } from './CloudOrganization'
import { Login } from './Login'
import { setApiBase } from '../api/config'
import { ThemeToggle } from './ThemeToggle'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient
const member: HubMember = {
  id: 'oidc:example', userId: 'kubeatlas:oidc:example', displayName: 'Example member',
  role: 'viewer', status: 'pending', clusterIds: [], kubernetesGroups: [],
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
}
const owner: HubMember = { ...member, id: 'portal:owner', userId: 'kubeatlas:portal:owner', displayName: 'Recovery owner', role: 'owner', status: 'active' }

beforeEach(() => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  setApiBase('/c/previous-cluster/api')
})
afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  setApiBase('/api')
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  document.documentElement.style.colorScheme = ''
  vi.unstubAllGlobals()
})
async function render(node: ReactNode) {
  await act(async () => root.render(<QueryClientProvider client={client}>{node}</QueryClientProvider>))
}

it('approves a pending member with explicit scope and warns about session/token invalidation', async () => {
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path === '/api/hub/members') return Response.json({ members: [owner, member] })
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', name: 'Alpha' }] })
    if (path === '/api/hub/members/oidc%3Aexample' && init?.method === 'PUT') return Response.json({ ...member, status: 'active', clusterIds: ['alpha'] })
    throw new Error(`Unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  await render(<CloudMembers member={owner} />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Example member')))
  expect(host.textContent).toContain('Recovery account')
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Review')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('revokes their existing MCP tokens')
  const save = [...dialog.querySelectorAll('button')].find(button => button.textContent === 'Save access')!
  expect(save.disabled).toBe(true)
  await act(async () => {
    for (const checkbox of dialog.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')) checkbox.click()
  })
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const mutation = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(mutation?.[0]).toBe('/api/hub/members/oidc%3Aexample')
  expect(JSON.parse(String(mutation?.[1]?.body))).toEqual({ role: 'viewer', status: 'active', clusterIds: ['alpha'], kubernetesGroups: [] })
})

it('does not fetch the member directory for viewers or cluster data for pending identities', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  await render(<CloudMembers member={{ ...member, status: 'active' }} />)
  expect(host.textContent).toContain('Only organization owners and administrators')
  await render(<PendingMembership member={member} retry={() => undefined} />)
  expect(host.textContent).toContain('Waiting for approval')
  expect(host.textContent).toContain('No cluster access or MCP credentials')
  expect(fetch).not.toHaveBeenCalled()
})

it('reports configured SSO separately from provider health and unsupported synchronization', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => Response.json({ enabled: true, issuer: 'https://identity.example/realms/atlas', clientId: 'portal', redirectUrl: 'https://portal.example/api/auth/oidc/callback', provisioning: 'approval', directorySync: false, backchannelLogout: false })))
  await render(<CloudSSO />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('https://identity.example/realms/atlas')))
  expect(host.textContent).toContain('Configured does not confirm current identity-provider availability')
  expect(host.textContent).toContain('does not immediately invalidate existing Hub sessions or MCP tokens')
  expect(host.textContent).not.toContain('Connected')
})

it('offers SSO through the fixed Portal route while retaining local recovery login', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => Response.json({ local: true, oidc: { enabled: true, loginUrl: 'https://untrusted.example' } }))
  vi.stubGlobal('fetch', fetch)
  await render(<Login />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('通过组织 SSO 登录')))
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/auth/providers')
  expect(host.querySelector('a[href="/api/auth/oidc/login"]')).not.toBeNull()
  expect(host.querySelector('input[autocomplete="current-password"]')).not.toBeNull()
  expect(host.innerHTML).not.toContain('untrusted.example')
})

it('retains the login theme on a fresh pending page and synchronizes native preferences', async () => {
  localStorage.setItem('radar-theme', 'light')
  await render(<ThemeToggle />)
  await act(async () => host.querySelector('button')!.click())
  expect(localStorage.getItem('kubeatlas-portal-theme')).toBe('dark')
  await render(null)
  document.documentElement.classList.remove('dark')
  document.documentElement.style.colorScheme = ''
  await render(<PendingMembership member={member} retry={() => undefined} />)
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  expect(document.documentElement.style.colorScheme).toBe('dark')
  expect(localStorage.getItem('radar-theme')).toBe('dark')
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="切换浅色主题"]')!.click())
  expect(document.documentElement.style.colorScheme).toBe('light')
  expect(localStorage.getItem('kubeatlas-portal-theme')).toBe('light')
  expect(localStorage.getItem('radar-theme')).toBe('light')
  await act(async () => window.dispatchEvent(new StorageEvent('storage', { key: 'radar-theme', newValue: 'dark' })))
  expect(document.documentElement.style.colorScheme).toBe('dark')
})

it('keeps existing native-theme preferences when no Portal preference exists', async () => {
  localStorage.setItem('radar-theme', 'dark')
  await render(<ThemeToggle />)
  expect(document.documentElement.style.colorScheme).toBe('dark')
  expect(localStorage.getItem('kubeatlas-portal-theme')).toBe('dark')
})
