// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { MCPCredentials } from './MCPCredentials'
import { Portal } from './Portal'
import { credentialStatus, openCodeFleetConfig } from './credentials'
import type { MCPCredential } from './credentials'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const credential: MCPCredential = {
  id: 'credential-1', label: 'OpenCode', clusterIds: ['east'], readOnly: true,
  createdAt: '2026-01-01T00:00:00Z', expiresAt: '2026-01-01T08:00:00Z', refreshUntil: '2026-01-31T00:00:00Z',
}
const cluster = { id: 'east', name: 'East' }
const response = (data: unknown) => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
const button = (label: string) => [...host.querySelectorAll('button')].find(button => button.textContent === label)!

beforeEach(() => {
  window.history.replaceState({}, '', '/mcp-access')
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('keeps MCP navigation hidden during server rendering until Hub status is checked', () => {
  const html = renderToStaticMarkup(<Portal />)
  expect(html).toContain('aria-label="门户导航"')
  expect(html).toContain('退出登录')
  expect(html).toContain('正在检查 Hub 模式')
  expect(html).not.toContain('href="/mcp-access"')
  expect(html).not.toContain('生成访问凭据')
  expect(html).not.toContain('搜索集群')
})

it('generates an OpenCode environment reference, rejects credential-bearing URLs, and distinguishes expiry', () => {
  const config = JSON.parse(openCodeFleetConfig('https://portal.example/mcp-fleet'))
  expect(config.mcp['kubeatlas-fleet']).toEqual({
    type: 'remote', url: 'https://portal.example/mcp-fleet', oauth: false,
    headers: { Authorization: 'Bearer {env:KUBEATLAS_MCP_TOKEN}' },
  })
  expect(() => openCodeFleetConfig('https://user:pass@portal.example/mcp-fleet')).toThrow()
  expect(() => openCodeFleetConfig('https://portal.example/mcp-fleet?token=secret')).toThrow()
  expect(credentialStatus(credential, Date.parse(credential.createdAt))).toBe('active')
  expect(credentialStatus(credential, Date.parse(credential.expiresAt))).toBe('expired')
  expect(credentialStatus({ ...credential, revokedAt: credential.createdAt }, 0)).toBe('revoked')
  expect(credentialStatus({ ...credential, expiresAt: 'invalid' }, 0)).toBe('unknown')
})

it('issues scoped credentials once, hides the secret after dismissal, and confirms revocation', async () => {
  let records: MCPCredential[] = []
  const fetch = vi.fn((url: string, options?: RequestInit) => {
    if (url === '/api/hub') return Promise.resolve(response({ enabled: true }))
    if (url === '/api/hub/clusters') return Promise.resolve(response({ clusters: [cluster, { id: 'revoked', name: 'Revoked', revokedAt: credential.createdAt }] }))
    if (url.endsWith('/revoke')) {
      records = [{ ...credential, revokedAt: credential.createdAt }]
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    if (options?.method === 'POST') {
      records = [credential]
      return Promise.resolve(response({ credential, token: 'unit-test-token-not-a-real-secret' }))
    }
    return Promise.resolve(response({ credentials: records, serverTime: credential.createdAt }))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Portal />))
  expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(1)
  expect(button('生成限时 Token').disabled).toBe(true)
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const post = fetch.mock.calls.find(([, options]) => options?.method === 'POST')!
  expect(JSON.parse(post[1]!.body as string)).toEqual({ label: 'OpenCode', clusterIds: ['east'], readOnly: true, ttlSeconds: 28800 })
  expect(host.querySelector<HTMLInputElement>('[aria-label="新生成的 MCP Token"]')!.type).toBe('password')
  expect(host.textContent).toContain('原文仅此次可见')
  expect(host.querySelector('pre')!.textContent).not.toContain('unit-test-token')
  expect(button('生成限时 Token').closest('fieldset')!.disabled).toBe(true)
  await act(async () => button('已保存，关闭原文').click())
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
  await act(async () => button('撤销凭据').click())
  expect(fetch.mock.calls.some(([url]) => url.endsWith('/revoke'))).toBe(false)
  await act(async () => button('确认撤销').click())
  expect(host.textContent).toContain('已撤销')
  expect(fetch.mock.calls.every(([url]) => url.startsWith('/api/mcp/credentials') || url === '/api/hub' || url === '/api/hub/clusters')).toBe(true)
})

it('surfaces uncertain creation failures without retrying the mutation', async () => {
  const fetch = vi.fn((url: string, options?: RequestInit) => url === '/api/hub/clusters'
    ? Promise.resolve(response({ clusters: [cluster] })) : options?.method === 'POST'
    ? Promise.reject(new Error('network interruption'))
    : Promise.resolve(response({ credentials: [], serverTime: credential.createdAt })))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<MCPCredentials />))
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(host.textContent).toContain('生成结果可能未确认')
  expect(fetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
})

it('fails closed when the Hub directory cannot be read, without falling back to registrations', async () => {
  const fetch = vi.fn((url: string, options?: RequestInit) => Promise.resolve(options?.method === 'POST'
    ? response({})
    : url === '/api/hub'
      ? response({ enabled: true })
      : url === '/api/hub/clusters'
        ? new Response(null, { status: 503 })
        : response({ credentials: [credential], serverTime: credential.createdAt })))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Portal />))
  expect(host.textContent).toContain('集群目录读取失败')
  expect(button('生成限时 Token').closest('fieldset')!.disabled).toBe(true)
  expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(fetch.mock.calls).toHaveLength(3)
  expect(fetch.mock.calls.some(([url]) => url === '/api/clusters')).toBe(false)
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false)
})

it('hides MCP navigation when Hub is disabled while keeping home directory polling', async () => {
  window.history.replaceState({}, '', '/')
  const fetch = vi.fn((url: string) => Promise.resolve(url === '/api/hub'
    ? new Response(null, { status: 404 })
    : response({ clusters: [], serverTime: credential.createdAt, staleAfterSeconds: 90 })))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Portal />))
  expect(host.querySelector('a[href="/mcp-access"]')).toBeNull()
  expect(fetch.mock.calls.some(([url]) => url === '/api/hub')).toBe(true)
  expect(fetch.mock.calls.some(([url]) => url === '/api/clusters')).toBe(true)
})

it('shows Hub-disabled guidance on direct credential visits without management requests', async () => {
  const fetch = vi.fn((url: string) => {
    expect(url).toBe('/api/hub')
    return Promise.resolve(new Response(null, { status: 404 }))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Portal />))
  expect(host.textContent).toContain('Hub 模式未启用')
  expect(host.textContent).toContain('--hub-state-file')
  expect(host.querySelector('a[href="/mcp-access"]')).toBeNull()
  expect(host.textContent).not.toContain('生成访问凭据')
  expect(fetch.mock.calls.map(([url]) => url)).toEqual(['/api/hub'])
})

it.each([
  { label: 'network failure', firstResponse: () => Promise.reject(new Error('network interruption')) },
  { label: '403 response', firstResponse: () => Promise.resolve(new Response(null, { status: 403 })) },
  { label: '5xx response', firstResponse: () => Promise.resolve(new Response(null, { status: 503 })) },
  { label: 'malformed metadata', firstResponse: () => Promise.resolve(response({ enabled: 'true' })) },
])('allows retry after $label during Hub availability lookup', async ({ firstResponse }) => {
  let availabilityRequests = 0
  const fetch = vi.fn((url: string) => {
    if (url === '/api/hub') {
      availabilityRequests++
      return availabilityRequests === 1 ? firstResponse() : Promise.resolve(response({ enabled: true }))
    }
    if (url === '/api/hub/clusters') return Promise.resolve(response({ clusters: [cluster] }))
    return Promise.resolve(response({ credentials: [], serverTime: credential.createdAt }))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Portal />))
  expect(host.textContent).toContain('无法确认 Hub 状态')
  expect(host.textContent).not.toContain('Hub 模式未启用')
  expect(host.querySelector('a[href="/mcp-access"]')).toBeNull()
  expect(fetch.mock.calls.map(([url]) => url)).toEqual(['/api/hub'])

  await act(async () => button('重试').click())
  expect(host.querySelector('a[href="/mcp-access"]')).not.toBeNull()
  expect(host.textContent).toContain('生成访问凭据')
  expect(fetch.mock.calls.some(([url]) => url === '/api/hub/clusters')).toBe(true)
  expect(fetch.mock.calls.some(([url]) => url === '/api/mcp/credentials')).toBe(true)
  expect(fetch.mock.calls.some(([url]) => url === '/api/clusters')).toBe(false)
})
