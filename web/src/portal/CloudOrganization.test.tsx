// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudMembers, CloudOrganization, CloudSSO, OrganizationSelection, PendingMembership, canManageOrganization, type HubMember } from './CloudOrganization'
import { CloudError } from './CloudShared'
import { Login } from './Login'
import { CloudRequestError, useCloudQuery } from './cloudApi'
import { setApiBase } from '../api/config'
import { ThemeToggle } from './ThemeToggle'
import { ConnectionProfilesProvider } from './ConnectionProfiles'
import { UnencryptedHttpWarning } from './CloudShared'
import { HUB_YAML_DRAFT_AUTHORITY_MARKER } from '@maomada/k8s-ui'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient
const member: HubMember = {
  id: 'member-1', userId: 'user-1', displayName: 'Example member', email: 'member@example.test',
  role: 'member', status: 'active',
}
const owner: HubMember = { ...member, id: 'owner-1', userId: 'owner-user', displayName: 'Organization owner', role: 'owner' }

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
function setValue(input: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}

function MutationCacheProbe() {
  useCloudQuery('/hub')
  useCloudQuery('/hub/organizations')
  useCloudQuery('/mcp/credentials')
  return null
}

it('keeps organization ownership separate from explicit platform authority', () => {
  expect(canManageOrganization(owner, false, 'org-1')).toBe(true)
  expect(canManageOrganization(owner, false, null)).toBe(false)
  expect(canManageOrganization(member, false, 'org-1')).toBe(false)
  expect(canManageOrganization({ ...owner, role: 'admin' as unknown as HubMember['role'] }, false, 'org-1')).toBe(false)
  expect(canManageOrganization(member, true, 'org-1')).toBe(true)
  expect(canManageOrganization(member, true, null)).toBe(false)
})

it('keeps a rejected logout visible on organization selection', async () => {
  sessionStorage.clear()
  sessionStorage.setItem('radar_yaml_draft:ConfigMap/qa/config', 'unsaved')
  sessionStorage.setItem(HUB_YAML_DRAFT_AUTHORITY_MARKER, 'authority-marker')
  let draftsWerePurgedBeforeRequest = false
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/auth/logout') {
      draftsWerePurgedBeforeRequest = sessionStorage.getItem('radar_yaml_draft:ConfigMap/qa/config') === null && sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER) === null
      return Response.json({ message: 'Logout unavailable' }, { status: 503 })
    }
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<OrganizationSelection organizations={[]} activeOrganizationId={null} platformOwner={false} />)
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '退出登录')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Logout unavailable')))
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/auth/logout')
  expect(draftsWerePurgedBeforeRequest).toBe(true)
})

it('shows structured request error codes and request IDs', async () => {
  await render(<CloudError error={new CloudRequestError('upstream timeout', 502, 'UPSTREAM_TIMEOUT', 'req-uncertain')} />)
  expect(host.textContent).toContain('错误代码：UPSTREAM_TIMEOUT')
  expect(host.textContent).toContain('请求 ID：req-uncertain')
})

it('updates organization name with only the frozen name field', async () => {
  let currentName = 'Original name'
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === '/api/hub' || String(url) === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (String(url) === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    if (String(url) === '/api/hub/organization' && init?.method === 'PUT') {
      currentName = 'Updated name'
      return Response.json({ id: 'org-1', name: currentName })
    }
    if (String(url) === '/api/hub/organization') return Response.json({ id: 'org-1', name: currentName })
    if (String(url) === '/api/hub/connections') return Response.json({ lanURL: '', publicURL: '', ownerUserID: 'owner', catalogAvailable: false, catalog: { available: false } })
    throw new Error('Unexpected request: ' + String(url))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<><ConnectionProfilesProvider><CloudOrganization member={owner} activeOrganizationId="org-1" /></ConnectionProfilesProvider><MutationCacheProbe /></>)
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLInputElement>('input')?.value).toBe('Original name')))
  const input = host.querySelector<HTMLInputElement>('input')!
  await act(async () => setValue(input, 'Updated name'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ name: 'Updated name' })
  await act(async () => vi.waitFor(() => {
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub').length).toBeGreaterThan(1)
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/organizations').length).toBeGreaterThan(1)
  }))
  expect(host.querySelector<HTMLInputElement>('input[placeholder="http://192.0.2.10"]')?.disabled).toBe(true)
  expect(host.textContent).toContain('不支持 HTTP 域名，也不会在 TLS 验证失败时回退')
  expect(host.textContent).toContain('本 Portal 不配置私有 CA 或节点信任')
})

it('updates only a member role and exposes no status, cluster-scope, or group controls', async () => {
  let updatedRole: HubMember['role'] = 'member'
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === '/api/hub') return Response.json({ enabled: true })
    if (String(url) === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (String(url) === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    if (String(url) === '/api/hub/members') return Response.json({ members: [owner, { ...member, role: updatedRole }] })
    if (String(url) === '/api/hub/members/member-1' && init?.method === 'PUT') {
      updatedRole = 'viewer'
      return Response.json({ member: { ...member, role: updatedRole } })
    }
    throw new Error('Unexpected request: ' + String(url))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<><CloudMembers member={owner} activeOrganizationId="org-1" /><MutationCacheProbe /></>)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Example member')))
  expect(host.textContent).toContain('最后一位组织 Owner 由服务器保护')
  expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
  expect(host.textContent).not.toContain('Kubernetes groups')
  const memberRow = [...host.querySelectorAll('tr')].find(row => row.textContent?.includes('Example member'))!
  const role = memberRow.querySelector<HTMLSelectElement>('select[aria-label="角色：Example member"]')!
  await act(async () => setValue(role, 'viewer'))
  await act(async () => [...memberRow.querySelectorAll('button')].find(button => button.textContent === '保存角色')!.click())
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ role: 'viewer' })
  await act(async () => vi.waitFor(() => {
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub').length).toBeGreaterThan(1)
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/mcp/credentials').length).toBeGreaterThan(1)
  }))
})

it('requires removal confirmation and sends DELETE without a mutation body', async () => {
  let removed = false
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === '/api/hub') return Response.json({ enabled: true })
    if (String(url) === '/api/hub/organizations') return Response.json({ organizations: [] })
    if (String(url) === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    if (String(url) === '/api/hub/members') return Response.json({ members: removed ? [owner] : [owner, member] })
    if (String(url) === '/api/hub/members/member-1' && init?.method === 'DELETE') {
      removed = true
      return new Response(null, { status: 204 })
    }
    throw new Error('Unexpected request: ' + String(url))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<><CloudMembers member={owner} activeOrganizationId="org-1" /><MutationCacheProbe /></>)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Example member')))
  const memberRow = [...host.querySelectorAll('tr')].find(row => row.textContent?.includes('Example member'))!
  const remove = [...memberRow.querySelectorAll('button')].find(button => button.textContent === '移除')!
  await act(async () => remove.click())
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain('撤销该组织范围内的会话和凭据')
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === '确认移除')!.click())
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'DELETE')
  expect(request?.[0]).toBe('/api/hub/members/member-1')
  expect(request?.[1]?.body).toBeUndefined()
  await act(async () => vi.waitFor(() => {
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub').length).toBeGreaterThan(1)
    expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/mcp/credentials').length).toBeGreaterThan(1)
  }))
})

it('does not fetch member records for viewers or pending identities', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  await render(<CloudMembers member={{ ...member, role: 'viewer' }} activeOrganizationId="org-1" />)
  expect(host.textContent).toContain('仅组织 Owner')
  await render(<PendingMembership member={{ ...member, status: 'pending' }} retry={() => undefined} />)
  expect(host.textContent).toContain('等待平台 Owner 审批')
  expect(host.textContent).toContain('不代表身份或邮箱已验证')
  expect(fetch).not.toHaveBeenCalled()
})

it('loads OIDC response details, fixed login link, approval boundary, and sync limitations', async () => {
  const configuration = {
    enabled: true,
    issuer: 'https://issuer.example.test',
    clientId: 'portal-client',
    redirectUrl: 'https://hub.example.test/api/auth/oidc/callback',
    provisioning: 'approval',
    directorySync: true,
    backchannelLogout: false,
  }
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/hub/sso') return Response.json(configuration)
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<CloudSSO />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('https://issuer.example.test')))
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/hub/sso')
  expect(host.textContent).toContain('portal-client')
  expect(host.textContent).toContain('https://hub.example.test/api/auth/oidc/callback')
  expect(host.textContent).toContain('配置已启用不代表身份提供方当前可用')
  expect(host.textContent).toContain('目录同步')
  expect(host.textContent).not.toContain('\uFFFD')
  expect(host.textContent).toContain('需平台 Owner 审批并分配组织成员资格')
  expect(host.textContent).toContain('外部目录组不会自动成为 Hub 组织成员资格')
  expect(host.textContent).toContain('身份提供方退出不会立即撤销现有 Hub 会话或 MCP Token')
  expect(host.querySelector('a[href="/api/auth/oidc/login"]')).not.toBeNull()
  expect(host.querySelector('input')).toBeNull()
})

it('shows unavailable OIDC configuration errors with a retry action', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/hub/sso') return Response.json({ message: 'SSO configuration unavailable', requestId: 'sso-42' }, { status: 503 })
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  await render(<CloudSSO />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('SSO configuration unavailable')))
  expect(host.textContent).toContain('sso-42')
  expect(host.querySelector('[role="alert"]')).not.toBeNull()
  expect(host.textContent).toContain('重试')
})

it('offers OIDC at the fixed Portal route and retains recovery username login', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => Response.json({ registrationEnabled: false, oidc: { enabled: true, loginUrl: 'https://untrusted.example' } }))
  vi.stubGlobal('fetch', fetch)
  await render(<Login />)
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('通过组织 SSO 登录')))
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/auth/providers')
  expect(host.querySelector('a[href="/api/auth/oidc/login"]')).not.toBeNull()
  expect(host.querySelector('input[autocomplete="username"]')).not.toBeNull()
  expect(host.querySelector('input[autocomplete="current-password"]')).not.toBeNull()
  expect(host.innerHTML).not.toContain('untrusted.example')
})

it('warns for non-loopback HTTP auth and enrollment origins only', async () => {
  await render(<><UnencryptedHttpWarning origin="http://hub.example.test" /><UnencryptedHttpWarning origin="http://127.0.0.1" /></>)
  expect(host.textContent).toContain('会话 Cookie 和 Token 可能被观察或篡改')
  expect(host.textContent).toContain('下载内容也可能被拦截或修改')
  expect(host.textContent).toContain('可信网络')
  expect(host.textContent).toContain('HTTP + 字面 IP 仅在服务器明确配置并启用相应支持后')
  expect(host.textContent).not.toContain('--insecure-cookie')
  expect(host.textContent).not.toContain('CA 证书')
  expect(host.querySelectorAll('[role="alert"]')).toHaveLength(1)
})

it('retains and synchronizes native theme preferences across pending pages', async () => {
  localStorage.setItem('radar-theme', 'light')
  await render(<ThemeToggle />)
  await act(async () => host.querySelector('button')!.click())
  expect(localStorage.getItem('kubeatlas-portal-theme')).toBe('dark')
  await render(null)
  document.documentElement.classList.remove('dark')
  document.documentElement.style.colorScheme = ''
  await render(<PendingMembership member={{ ...member, status: 'pending' }} retry={() => undefined} />)
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
