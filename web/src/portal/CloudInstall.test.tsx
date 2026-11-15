// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudInstall } from './CloudClusters'
import { ConnectionProfilesProvider } from './ConnectionProfiles'
import { setApiBase } from '../api/config'
import type { HubContext } from './CloudOrganization'
import type { HubCluster } from './cloudApi'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient
const hub: HubContext = {
  enabled: true, userId: 'user-1', authorizationRevision: '1',
  member: { id: 'member-1', userId: 'user-1', displayName: 'Owner', role: 'owner', status: 'active' },
  platformOwner: false, activeOrganizationId: 'org-1',
  organizations: [{ id: 'org-1', name: 'Example organization', role: 'owner' }],
}
const profile = (bootstrapAvailable?: boolean) => ({
  lanURL: 'https://lan.example.test', publicURL: 'https://public.example.test',
  ownerUserID: 'owner', catalogAvailable: true, catalog: { available: true },
  ...(bootstrapAvailable === undefined ? {} : { bootstrapAvailable }),
})
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const revokedCluster: HubCluster = {
  id: 'cluster-revoked', organizationId: 'org-1', name: 'Demo east', state: 'revoked',
  environment: '', region: '', description: '', createdAt: '', updatedAt: '', configured: true, revokedAt: '2026-01-01T00:00:00Z',
}
const staleConnectedCluster: HubCluster = { ...revokedCluster, state: 'connected', revokedAt: undefined }
const refreshClusters = vi.fn()

beforeEach(() => {
  window.history.replaceState({}, '', '/install')
  localStorage.removeItem('kubeatlas.hub.connection-profile')
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  setApiBase('/c/previous-cluster/api')
  refreshClusters.mockClear()
})
afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  setApiBase('/api')
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
async function renderInstall(clusters: HubCluster[] = [], canManage = true, directoryError?: unknown, portalHub: HubContext = hub) {
  await act(async () => root.render(<QueryClientProvider client={client}><ConnectionProfilesProvider><CloudInstall hub={portalHub} clusters={clusters} canManage={canManage} directoryError={directoryError} onRefreshClusters={refreshClusters} /></ConnectionProfilesProvider></QueryClientProvider>))
}
function setValue(input: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}
function installFetch(handler: (path: string, init?: RequestInit) => Response | Promise<Response>, connections = profile()) {
  const fetch = vi.fn<typeof globalThis.fetch>((input, init) => {
    const path = String(input)
    if (path === '/api/hub/connections') return Promise.resolve(response(connections))
    return Promise.resolve(handler(path, init))
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

it('requests a configured server command and invalidates it across profile changes', async () => {
  let commandNumber = 0
  const command = 'kubeatlas agent enroll --version v1.4.2 --sha256 abc123 --image registry.example/agent@sha256:abc123 --origin https://lan.example.test'
  const fetch = installFetch(path => {
    if (path === '/api/hub/enrollments/command') {
      commandNumber++
      return response({ command, origin: 'https://lan.example.test' })
    }
    throw new Error('Unexpected request: ' + path)
  })
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('--version v1.4.2 --sha256 abc123')))
  const request = fetch.mock.calls.find(([url]) => String(url) === '/api/hub/enrollments/command')
  expect(request?.[1]?.method).toBe('POST')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ profile: 'lan' })
  expect(host.textContent).toContain('Hub 地址：https://lan.example.test')
  expect(host.querySelector('pre')?.textContent).toBe(command)
  expect(host.textContent).toContain('--image registry.example/agent@sha256:abc123')
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '公网')!.click())
  expect(host.textContent).not.toContain('kubeatlas agent enroll')
  expect(commandNumber).toBe(1)
})

it('ignores a command response from a profile that changed while the request was pending', async () => {
  let resolveOld!: (result: Response) => void
  const oldCommand = new Promise<Response>(resolve => { resolveOld = resolve })
  let commands = 0
  const fetch = installFetch(path => {
    if (path === '/api/hub/enrollments/command') {
      commands++
      return commands === 1 ? oldCommand : response({ command: 'kubeatlas agent enroll --origin https://public.example.test', origin: 'https://public.example.test' })
    }
    throw new Error('Unexpected request: ' + path)
  })
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(commands).toBe(1)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '公网')!.click())
  await act(async () => { resolveOld(response({ command: 'kubeatlas agent enroll --origin https://lan.example.test', origin: 'https://lan.example.test' })); await Promise.resolve() })
  expect(host.textContent).not.toContain('kubeatlas agent enroll')
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('https://public.example.test')))
  expect(commands).toBe(2)
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command').map(([, init]) => JSON.parse(init?.body as string))).toEqual([{ profile: 'lan' }, { profile: 'public' }])
})

it('preserves AbortError when a profile change cancels command generation', async () => {
  let requestSignal: AbortSignal | undefined
  const fetch = installFetch((path, init) => {
    if (path !== '/api/hub/enrollments/command') throw new Error('Unexpected request: ' + path)
    requestSignal = init?.signal ?? undefined
    return new Promise<Response>((_, reject) => {
      requestSignal?.addEventListener('abort', () => reject(new DOMException('Aborted by profile change', 'AbortError')), { once: true })
    })
  })
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(requestSignal).toBeDefined()))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '公网')!.click())
  await act(async () => vi.waitFor(() => expect(requestSignal?.aborted).toBe(true)))
  await act(async () => Promise.resolve())
  expect(host.textContent).not.toContain('Aborted by profile change')
  expect(host.textContent).not.toContain('Failed to fetch')
  expect(host.textContent).not.toContain('网络请求失败')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command')).toHaveLength(1)
})

it('fails closed for unavailable bootstrap data and does not issue a command', async () => {
  const fetch = installFetch(path => { throw new Error('Unexpected request: ' + path) }, profile(false))
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('引导目录不可用')))
  expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(true)
  const connections = fetch.mock.calls.find(([url]) => String(url) === '/api/hub/connections')
  expect(connections).toBeDefined()
  expect(host.textContent).toContain('引导目录不可用')
  expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub/enrollments/command')).toBe(false)
})

it('shows command errors without retrying', async () => {
  const fetch = installFetch(path => path === '/api/hub/enrollments/command'
    ? response({ message: 'bootstrap unavailable' }, 503)
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('bootstrap unavailable')))
  expect(host.textContent).not.toContain('无法连接到 Hub')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command')).toHaveLength(1)
})

it('localizes a browser transport failure for enrollment command requests', async () => {
  const fetch = installFetch(path => path === '/api/hub/enrollments/command'
    ? Promise.reject(new TypeError('Failed to fetch'))
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('网络请求失败，请检查网络连接后重试。')))
  expect(host.textContent).not.toContain('Failed to fetch')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command')).toHaveLength(1)
})

it('rejects empty commands and commands for a different origin', async () => {
  const responses = [
    { command: '  ', origin: 'https://lan.example.test' },
    { command: 'kubeatlas agent enroll', origin: 'https://other.example.test' },
  ]
  let commandIndex = 0
  const fetch = installFetch(path => path === '/api/hub/enrollments/command'
    ? response(responses[commandIndex++])
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  const generate = () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!
  await act(async () => generate().click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Hub 返回的注册命令不完整')))
  expect(host.textContent).not.toContain('kubeatlas agent enroll')
  await act(async () => generate().click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('命令地址与当前选定的连接配置不匹配')))
  expect(host.textContent).not.toContain('kubeatlas agent enroll')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command')).toHaveLength(2)
})

it('never renders a server command containing an obvious credential argument', async () => {
  const fetch = installFetch(path => path === '/api/hub/enrollments/command'
    ? response({ command: 'kubeatlas agent enroll --token do-not-render-this', origin: 'https://lan.example.test' })
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(false)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '生成注册命令')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('疑似凭据参数')))
  expect(host.textContent).not.toContain('do-not-render-this')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/command')).toHaveLength(1)
})

it('requires management permission and an active organization for commands and lookups', async () => {
  const fetch = installFetch(path => Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall([], false)
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'ABCD'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(true)
  expect(fetch.mock.calls.some(([url]) => ['/api/hub/enrollments/command', '/api/hub/enrollments/lookup'].includes(String(url)))).toBe(false)

  await renderInstall([], true, undefined, { ...hub, activeOrganizationId: null })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(host.querySelector<HTMLButtonElement>('button.cloud-primary')?.disabled).toBe(true)
  expect(fetch.mock.calls.some(([url]) => ['/api/hub/enrollments/command', '/api/hub/enrollments/lookup'].includes(String(url)))).toBe(false)
})

it('looks up and approves a pairing without displaying secrets or claiming connection', async () => {
  window.history.replaceState({}, '', '/install?clusterId=cluster-revoked')
  const fetch = installFetch((path, init) => {
    if (path === '/api/hub/enrollments/lookup') return response({
      id: 'enrollment-1', userCode: 'ABCD-1234', contextName: 'Demo east',
      clusterFingerprint: 'sha256:abc123', status: 'pending', expiresAt: '2099-01-01T00:00:00Z',
      deviceSecret: 'never-render-this-device-secret', agentToken: 'never-render-this-token',
    })
    if (path === '/api/hub/enrollments/enrollment-1/approve') return response({ id: 'cluster-revoked', status: 'approved' })
    throw new Error('Unexpected request: ' + path + ' ' + String(init?.method))
  })
  await renderInstall([revokedCluster])
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'ABCD-1234'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
  expect(host.textContent).toContain('sha256:abc123')
  expect(host.textContent).toContain('ABCD-1234')
  expect(host.textContent).toContain('待处理')
  expect(host.textContent).not.toContain('never-render-this')
  const lookup = fetch.mock.calls.find(([url]) => String(url) === '/api/hub/enrollments/lookup')
  expect(JSON.parse(lookup?.[1]?.body as string)).toEqual({ userCode: 'ABCD-1234' })
  await act(async () => vi.waitFor(() => expect(host.querySelector<HTMLSelectElement>('select.portal-control')).not.toBeNull()))
  const reEnrollment = host.querySelector<HTMLSelectElement>('select.portal-control')!
  expect(reEnrollment.value).toBe('')
  await act(async () => setValue(reEnrollment, 'cluster-revoked'))
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '批准配对')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对已批准')))
  const approval = fetch.mock.calls.find(([url]) => String(url) === '/api/hub/enrollments/enrollment-1/approve')
  expect(JSON.parse(approval?.[1]?.body as string)).toEqual({ userCode: 'ABCD-1234', organizationId: 'org-1', name: 'Demo east', clusterId: 'cluster-revoked' })
  const liveRows = [...host.querySelectorAll('.cloud-live-cluster')]
  expect(liveRows).toHaveLength(1)
  expect(liveRows[0].textContent).toContain('cluster-revoked')
  expect(liveRows[0].textContent).toContain('已撤销')
  expect(liveRows.some(row => row.textContent?.includes('已连接'))).toBe(false)
  expect(host.textContent).toContain('批准不代表 Agent 已安装或已连接')
})

it('clears a reviewed pairing and confirmation when the input code changes', async () => {
  let lookups = 0
  const fetch = installFetch(path => {
    if (path === '/api/hub/enrollments/lookup') {
      lookups++
      const userCode = lookups === 1 ? 'ABCD-1234' : 'WXYZ-5678'
      return response({ id: 'enrollment-' + lookups, userCode, contextName: 'Demo east', clusterFingerprint: 'sha256:abc123', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' })
    }
    if (path === '/api/hub/enrollments/enrollment-2/approve') return response({ id: 'cluster-2', status: 'approved' })
    throw new Error('Unexpected request: ' + path)
  })
  await renderInstall()
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  const search = async (value: string) => {
    await act(async () => setValue(code, value))
    await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain(value)))
  }
  await search('ABCD-1234')
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  expect([...host.querySelectorAll('button')].find(button => button.textContent === '批准配对')?.disabled).toBe(false)

  await act(async () => setValue(code, 'WXYZ-5678'))
  expect(host.textContent).not.toContain('ABCD-1234')
  expect([...host.querySelectorAll('button')].find(button => button.textContent === '批准配对')).toBeUndefined()
  expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/approve'))).toHaveLength(0)

  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(lookups).toBe(2)))
  expect(host.textContent).toContain('WXYZ-5678')
  const approve = [...host.querySelectorAll('button')].find(button => button.textContent === '批准配对')!
  expect(approve.disabled).toBe(true)
  await act(async () => approve.click())
  expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/approve'))).toHaveLength(0)

  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '批准配对')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对已批准')))
  const approval = fetch.mock.calls.find(([url]) => String(url).endsWith('/enrollment-2/approve'))
  expect(JSON.parse(approval?.[1]?.body as string).userCode).toBe('WXYZ-5678')
})

it('denies only an explicitly looked-up pending code and sends the exact body', async () => {
  const fetch = installFetch(path => {
    if (path === '/api/hub/enrollments/lookup') return response({ id: 'enrollment-2', userCode: 'WXYZ', contextName: 'West', clusterFingerprint: 'fingerprint', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' })
    if (path === '/api/hub/enrollments/enrollment-2/deny') return new Response(null, { status: 204 })
    throw new Error('Unexpected request: ' + path)
  })
  await renderInstall()
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'WXYZ'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
  await act(async () => vi.waitFor(() => expect([...host.querySelectorAll('button')].some(button => button.textContent === '拒绝配对')).toBe(true)))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '拒绝配对')!.click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对已拒绝')))
  const denial = fetch.mock.calls.find(([url]) => String(url).endsWith('/deny'))
  expect(JSON.parse(denial?.[1]?.body as string)).toEqual({ userCode: 'WXYZ' })
})

it.each(['approve', 'deny'])('requires a fresh lookup after uncertain %s mutation', async action => {
  let lookups = 0
  const fetch = installFetch(path => {
    if (path === '/api/hub/enrollments/lookup') {
      lookups++
      return response({ id: 'enrollment-' + lookups, userCode: lookups === 1 ? 'UNCERTAIN' : 'FRESH', contextName: 'North', clusterFingerprint: 'fingerprint', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' })
    }
    if (path === '/api/hub/enrollments/enrollment-1/' + action) return response({ message: 'upstream timeout', error_code: 'UPSTREAM_TIMEOUT', requestId: 'req-uncertain' }, 502)
    throw new Error('Unexpected request: ' + path)
  })
  await renderInstall()
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'UNCERTAIN'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
  const actionButton = () => [...host.querySelectorAll('button')].find(button => button.textContent === (action === 'approve' ? '批准配对' : '拒绝配对'))!
  await act(async () => vi.waitFor(() => expect(actionButton()).toBeTruthy()))
  if (action === 'approve') await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => actionButton().click())
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('系统不会自动重试配对操作')))
  expect(host.textContent).toContain('错误代码：UPSTREAM_TIMEOUT')
  expect(host.textContent).toContain('请求 ID：req-uncertain')
  expect(actionButton().disabled).toBe(true)
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/enrollment-1/' + action)).toHaveLength(1)
  await act(async () => setValue(code, 'FRESH'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(lookups).toBe(2)))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
  if (action === 'approve') await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => vi.waitFor(() => expect(actionButton().disabled).toBe(false)))
  expect(host.querySelector('.cloud-live-cluster')).toBeNull()
})

it('fails closed when pairing context or fingerprint is missing', async () => {
  const fetch = installFetch(path => path === '/api/hub/enrollments/lookup'
    ? response({ id: 'incomplete', userCode: 'CODE', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' })
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'CODE'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对查询信息不完整')))
  expect(host.textContent).not.toContain('配对审核')
  expect(fetch.mock.calls.filter(([url]) => String(url) === '/api/hub/enrollments/lookup')).toHaveLength(1)
})

it('updates pairing expiry while review is open', async () => {
  const expiresAt = new Date(Date.now() + 700).toISOString()
  const fetch = installFetch(path => path === '/api/hub/enrollments/lookup'
    ? response({ id: 'expiring', userCode: 'SOON', contextName: 'North', clusterFingerprint: 'fingerprint', status: 'pending', expiresAt })
    : Promise.reject(new Error('Unexpected request: ' + path)))
  await renderInstall()
  const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
  await act(async () => setValue(code, 'SOON'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
  await act(async () => new Promise(resolve => window.setTimeout(resolve, 2200)))
  expect(host.textContent).toContain('已过期或有效期无效')
  expect([...host.querySelectorAll('button')].some(button => button.textContent === '批准配对' || button.textContent === '拒绝配对')).toBe(false)
  expect(fetch.mock.calls.some(([url]) => String(url).includes('/approve') || String(url).includes('/deny'))).toBe(false)
})

it('keeps directory errors unknown and offers an explicit live status refresh', async () => {
  const fetch = installFetch(path => { throw new Error('Unexpected request: ' + path) })
  await renderInstall([staleConnectedCluster], true, new Error('directory status unavailable'))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('directory status unavailable')))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '刷新状态')!.click())
  expect(refreshClusters).toHaveBeenCalledOnce()
  expect(host.querySelector('.cloud-live-cluster')).toBeNull()
  expect(fetch.mock.calls.some(([url]) => String(url) === '/api/hub/enrollments/lookup')).toBe(false)
})

it.each(['pending', 'used'])('disables mutations for %s pairing results when expired or used', async status => {
    const fetch = installFetch(path => {
      if (path === '/api/hub/enrollments/lookup') return response({ id: 'enrollment-expired', userCode: 'EXPIRED', contextName: 'Demo', clusterFingerprint: 'fingerprint', status, expiresAt: '2000-01-01T00:00:00Z' })
      throw new Error('Unexpected request: ' + path)
    })
    await renderInstall()
    const code = host.querySelector<HTMLInputElement>('input[maxlength="128"]')!
    await act(async () => setValue(code, 'EXPIRED'))
    await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('配对审核')))
    expect([...host.querySelectorAll('button')].some(button => button.textContent === '批准配对' || button.textContent === '拒绝配对')).toBe(false)
    expect(host.textContent).not.toContain('deviceSecret')
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'PUT' || init?.method === 'DELETE')).toBe(false)
})
