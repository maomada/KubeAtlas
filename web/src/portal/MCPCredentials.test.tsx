// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { copyText } from '@maomada/k8s-ui/utils/clipboard'
import { MCPCredentials } from './MCPCredentials'
import { ConnectionProfilesProvider } from './ConnectionProfiles'
import type { MCPCredential } from './credentials'

vi.mock('@maomada/k8s-ui/utils/clipboard', () => ({ copyText: vi.fn() }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const credential: MCPCredential = {
  id: 'credential-1', organizationId: 'org-1', label: '我的助手', clusterIds: ['east'], readOnly: true,
  createdAt: '2026-01-01T00:00:00Z', expiresAt: '2026-01-01T08:00:00Z', refreshUntil: '2026-01-31T00:00:00Z',
}
const cluster = { id: 'east', name: 'East' }
const response = (data: unknown) => new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
const button = (label: string) => [...host.querySelectorAll('button')].find(button => button.textContent === label)!
const secret = 'unit-test-token-not-a-real-secret'
const card = (label = credential.label) => host.querySelector(`[aria-label="凭据 ${label}"]`)
const nameInput = () => [...host.querySelectorAll('label')].find(label => label.textContent?.startsWith('名称'))!.querySelector('input')!
function setName(value: string) {
  const input = nameInput()
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}
async function submit() {
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
}
async function mount(organizationId = 'org-1') {
  await act(async () => root.render(<ConnectionProfilesProvider key={organizationId}><MCPCredentials organizationId={organizationId} organizationName="Example organization" /></ConnectionProfilesProvider>))
}
function fixtureFetch(override: (url: string, options?: RequestInit) => Response | Promise<Response> | undefined = () => undefined) {
  const fetch = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    const url = String(input)
    const result = override(url, options)
    if (result !== undefined) return result
    if (url === '/api/hub/connections') return response({ lanURL: 'https://hub.example.test', publicURL: '' })
    if (url === '/api/hub/clusters') return response({ clusters: [cluster] })
    if (url.endsWith('/revoke')) return new Response(null, { status: 204 })
    if (url === '/api/mcp/credentials' && options?.method === 'POST') return response({ credential, token: secret })
    return response({ credentials: [credential], serverTime: credential.createdAt })
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

beforeEach(() => {
  window.localStorage.removeItem('kubeatlas.hub.connection-profile')
  window.history.replaceState({}, '', '/mcp-access')
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  vi.mocked(copyText).mockReset().mockResolvedValue(true)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('limits viewer credential controls to read-only regardless of the default write option', () => {
  const html = renderToStaticMarkup(<ConnectionProfilesProvider><MCPCredentials readOnlyOnly organizationId="org-1" organizationName="Example organization" /></ConnectionProfilesProvider>)
  expect(html).not.toContain('value="write"')
  expect(html).toContain('value="read"')
  expect(html).toContain('disabled=""')
  expect(html).toContain('只读 · 查询与诊断')
})

it('distinguishes identically named clusters before issuing a scoped credential', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const url = String(input)
    if (url === '/api/hub/connections') return response({ lanURL: 'https://hub.example.test', publicURL: '' })
    if (url === '/api/hub') return response({ enabled: true })
    if (url === '/api/hub/clusters') return response({ clusters: [{ id: 'east', name: 'Same name' }, { id: 'west', name: 'Same name' }] })
    if (init?.method === 'POST') return response({ credential: { ...credential, clusterIds: ['west'] }, token: 'unit-test-only-token' })
    return response({ credentials: [], serverTime: credential.createdAt })
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><MCPCredentials organizationId="org-1" organizationName="Example organization" /></ConnectionProfilesProvider>))
  const choices = host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
  expect(choices).toHaveLength(2)
  expect(choices[0].labels?.[0].textContent).toContain('Same name · east')
  expect(choices[1].labels?.[0].textContent).toContain('Same name · west')
  await act(async () => setName('  我的助手  '))
  await act(async () => choices[1].click())
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const post = fetch.mock.calls.find(([, init]) => init?.method === 'POST')
  expect(JSON.parse(post?.[1]?.body as string).clusterIds).toEqual(['west'])
  expect(JSON.parse(post?.[1]?.body as string).label).toBe('我的助手')
})

it('issues scoped credentials once, hides the secret after dismissal, and confirms revocation', async () => {
  let records: MCPCredential[] = []
  const fetch = vi.fn((url: string, options?: RequestInit) => {
    if (url === '/api/hub/connections') return Promise.resolve(response({ lanURL: 'https://hub.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: false, catalog: { available: false } }))
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
  await act(async () => root.render(<ConnectionProfilesProvider><MCPCredentials organizationId="org-1" organizationName="Example organization" /></ConnectionProfilesProvider>))
  expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(1)
  expect(button('生成限时 Token').disabled).toBe(true)
  await act(async () => setName('我的助手'))
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const post = fetch.mock.calls.find(([, options]) => options?.method === 'POST')!
  expect(JSON.parse(post[1]!.body as string)).toEqual({ label: '我的助手', clusterIds: ['east'], readOnly: true, ttlSeconds: 28800 })
  expect(host.textContent).toContain('当前组织：Example organization')
  expect(host.querySelector<HTMLInputElement>('[aria-label="新生成的 MCP Token"]')!.type).toBe('password')
  expect(host.textContent).toContain('原文仅此次可见')
  expect(host.querySelector('pre')).toBeNull()
  expect(host.textContent).toContain('连接 AI Agent')
  expect(host.textContent).not.toContain('OpenCode')
  expect(button('复制连接信息')).toBeDefined()
  const copyStatus = button('复制连接信息').parentElement!.querySelector('[role="status"]')!
  expect(copyStatus.textContent).toBe('')
  await act(async () => button('复制连接信息').click())
  expect(copyText).toHaveBeenLastCalledWith(`Transport: Streamable HTTP\nURL: https://hub.example.test/mcp\nAuthorization: Bearer ${secret}`)
  expect([...host.querySelectorAll('[role="status"]')].some(node => node.textContent === '已复制')).toBe(true)
  const firstAnnouncement = copyStatus.textContent
  await act(async () => button('复制连接信息').click())
  expect(copyStatus.textContent).not.toBe(firstAnnouncement)
  expect(copyStatus.textContent).not.toContain(secret)
  expect(button('生成限时 Token').closest('fieldset')!.disabled).toBe(true)
  await act(async () => button('已保存，关闭原文').click())
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
  expect(button('复制连接信息')).toBeUndefined()
  expect(host.innerHTML).not.toContain(secret)
  expect(window.localStorage.getItem('KUBEATLAS_MCP_TOKEN')).toBeNull()
  await act(async () => button('撤销凭据').click())
  expect(fetch.mock.calls.some(([url]) => url.endsWith('/revoke'))).toBe(false)
  await act(async () => button('确认撤销').click())
  expect(card()).toBeNull()
  expect(host.textContent).not.toContain('已撤销')
  expect(fetch.mock.calls.every(([url]) => url.startsWith('/api/mcp/credentials') || url === '/api/hub' || url === '/api/hub/clusters' || url === '/api/hub/connections')).toBe(true)
})

it('surfaces uncertain creation failures without retrying the mutation', async () => {
  const fetch = vi.fn((url: string, options?: RequestInit) => url === '/api/hub/connections'
    ? Promise.resolve(response({ lanURL: 'https://hub.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: false, catalog: { available: false } })) : url === '/api/hub/clusters'
    ? Promise.resolve(response({ clusters: [cluster] })) : options?.method === 'POST'
    ? Promise.reject(new Error('network interruption'))
    : Promise.resolve(response({ credentials: [], serverTime: credential.createdAt })))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><MCPCredentials organizationId="org-1" organizationName="Example organization" /></ConnectionProfilesProvider>))
  await act(async () => setName('我的助手'))
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(host.textContent).toContain('生成结果可能未确认')
  expect(fetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
})

it('fails closed when the Hub directory cannot be read, without falling back to registrations', async () => {
  const fetch = vi.fn((url: string, options?: RequestInit) => Promise.resolve(url === '/api/hub/connections'
    ? response({ lanURL: '', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: false, catalog: { available: false } })
    : options?.method === 'POST'
    ? response({})
    : url === '/api/hub'
      ? response({ enabled: true })
      : url === '/api/hub/clusters'
        ? new Response(null, { status: 503 })
        : response({ credentials: [credential], serverTime: credential.createdAt })))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><MCPCredentials organizationId="org-1" organizationName="Example organization" /></ConnectionProfilesProvider>))
  expect(host.textContent).toContain('集群目录读取失败')
  expect(button('生成限时 Token').closest('fieldset')!.disabled).toBe(true)
  expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(fetch.mock.calls).toHaveLength(3)
  expect(fetch.mock.calls.some(([url]) => url === '/api/clusters')).toBe(false)
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false)
})

it('starts with an empty required name and rejects empty or whitespace-only submissions', async () => {
  const fetch = fixtureFetch()
  await mount()
  expect(nameInput().value).toBe('')
  expect(nameInput().required).toBe(true)
  expect(nameInput().placeholder).not.toContain('OpenCode')
  await act(async () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  expect(button('生成限时 Token').disabled).toBe(true)
  await submit()
  await act(async () => setName('   '))
  expect(button('生成限时 Token').disabled).toBe(true)
  await submit()
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false)
  await act(async () => setName('  自定义名称  '))
  expect(button('生成限时 Token').disabled).toBe(false)
  await submit()
  const posts = fetch.mock.calls.filter(([, options]) => options?.method === 'POST')
  expect(posts).toHaveLength(1)
  expect(JSON.parse(posts[0][1]!.body as string)).toMatchObject({ label: '自定义名称', clusterIds: ['east'], readOnly: true })
})

it('hides retained revoked records on initial load while keeping expired credentials visible', async () => {
  fixtureFetch(url => url === '/api/mcp/credentials' ? response({
    credentials: [
      credential,
      { ...credential, id: 'revoked', label: 'Revoked client', revokedAt: credential.createdAt },
      { ...credential, id: 'expired', label: 'Expired client', expiresAt: credential.createdAt },
    ], serverTime: credential.createdAt,
  }) : undefined)
  await mount()
  expect(card()).not.toBeNull()
  expect(card('Revoked client')).toBeNull()
  expect(card('Expired client')).not.toBeNull()
  expect(card('Expired client')?.textContent).toContain('已过期')
  expect(host.textContent).not.toContain('已撤销')
})

it('shows the empty state when the server retains only revoked credential records', async () => {
  fixtureFetch(url => url === '/api/mcp/credentials' ? response({
    credentials: [{ ...credential, revokedAt: credential.createdAt }], serverTime: credential.createdAt,
  }) : undefined)
  await mount()
  expect(card()).toBeNull()
  expect(host.textContent).toContain('暂无凭据')
})

it('removes a confirmed revocation and its shown secret even if the following reload fails', async () => {
  let revoked = false
  const other = { ...credential, id: 'credential-2', label: 'Other client' }
  const fetch = fixtureFetch((url, options) => {
    if (url.endsWith('/revoke')) {
      revoked = true
      return new Response(null, { status: 204 })
    }
    if (url === '/api/mcp/credentials' && options?.method !== 'POST') return revoked
      ? new Response(JSON.stringify({ message: 'fixture reload unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } })
      : response({ credentials: [credential, other], serverTime: credential.createdAt })
    return undefined
  })
  await mount()
  await act(async () => {
    setName('我的助手')
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click()
  })
  await submit()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).not.toBeNull()
  await act(async () => card()!.querySelector<HTMLButtonElement>('button')!.click())
  await act(async () => button('确认撤销').click())
  expect(card()).toBeNull()
  expect(card('Other client')).not.toBeNull()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
  expect(button('复制连接信息')).toBeUndefined()
  expect(host.innerHTML).not.toContain(secret)
  expect(host.textContent).toContain('fixture reload unavailable')
  expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/revoke'))).toHaveLength(1)
})

it('does not revive a confirmed revoked credential from a late or stale list response', async () => {
  let listCalls = 0
  let resolveOldList!: (value: Response) => void
  let oldSignal: AbortSignal | null | undefined
  fixtureFetch((url, options) => {
    if (url !== '/api/mcp/credentials' || options?.method === 'POST') return undefined
    listCalls++
    if (listCalls === 2) {
      oldSignal = options?.signal
      return new Promise<Response>(resolve => { resolveOldList = resolve })
    }
    return response({ credentials: [credential], serverTime: credential.createdAt })
  })
  await mount()
  await act(async () => button('刷新列表').click())
  expect(listCalls).toBe(2)
  await act(async () => button('撤销凭据').click())
  await act(async () => button('确认撤销').click())
  expect(listCalls).toBe(3)
  expect(oldSignal?.aborted).toBe(true)
  expect(card()).toBeNull()
  await act(async () => resolveOldList(response({ credentials: [credential], serverTime: credential.createdAt })))
  expect(card()).toBeNull()
  await act(async () => button('刷新列表').click())
  expect(card()).toBeNull()
})

it('keeps a credential visible and reports uncertainty when revocation fails', async () => {
  const fetch = fixtureFetch(url => url.endsWith('/revoke')
    ? new Response(JSON.stringify({ message: 'fixture revoke unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } })
    : undefined)
  await mount()
  await act(async () => button('撤销凭据').click())
  await act(async () => button('确认撤销').click())
  expect(card()).not.toBeNull()
  expect(host.textContent).toContain('fixture revoke unavailable')
  expect(host.textContent).toContain('撤销结果未确认')
  expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/revoke'))).toHaveLength(1)
})

it('announces copy failures without claiming the connection details were copied', async () => {
  fixtureFetch()
  vi.mocked(copyText).mockResolvedValue(false)
  await mount()
  await act(async () => button('复制地址').click())
  expect(copyText).toHaveBeenCalledWith('https://hub.example.test/mcp')
  expect([...host.querySelectorAll('[role="status"]')].some(node => node.textContent?.includes('复制失败'))).toBe(true)
  expect([...host.querySelectorAll('[role="status"]')].some(node => node.textContent === '已复制')).toBe(false)
})

it('does not offer secret-bearing connection details for an invalid endpoint', async () => {
  fixtureFetch(url => url === '/api/hub/connections' ? response({ lanURL: 'https://user:pass@hub.example.test', publicURL: '' }) : undefined)
  await mount()
  await act(async () => {
    setName('我的助手')
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click()
  })
  await submit()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).not.toBeNull()
  expect(button('复制连接信息')).toBeUndefined()
  expect(copyText).not.toHaveBeenCalled()
})

it('clears the issued token, form and old credential list when the organization provider remounts', async () => {
  let organizationId = 'org-1'
  fixtureFetch((url, options) => url === '/api/mcp/credentials' && options?.method !== 'POST'
    ? response({ credentials: organizationId === 'org-1' ? [credential] : [], serverTime: credential.createdAt })
    : undefined)
  await mount()
  await act(async () => {
    setName('我的助手')
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click()
  })
  await submit()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).not.toBeNull()
  organizationId = 'org-2'
  await mount(organizationId)
  expect(nameInput().value).toBe('')
  expect(card()).toBeNull()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
  expect(button('复制连接信息')).toBeUndefined()
  expect(host.innerHTML).not.toContain(secret)
  expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(false)
})

it('clears a shown token after refresh observes its revocation from another client', async () => {
  let externallyRevoked = false
  fixtureFetch((url, options) => url === '/api/mcp/credentials' && options?.method !== 'POST'
    ? response({ credentials: [{ ...credential, ...(externallyRevoked ? { revokedAt: credential.createdAt } : {}) }], serverTime: credential.createdAt })
    : undefined)
  await mount()
  await act(async () => {
    setName('我的助手')
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click()
  })
  await submit()
  expect(button('复制连接信息')).toBeDefined()
  externallyRevoked = true
  await act(async () => button('刷新列表').click())
  expect(card()).toBeNull()
  expect(button('复制连接信息')).toBeUndefined()
  expect(host.querySelector('[aria-label="新生成的 MCP Token"]')).toBeNull()
  expect(host.innerHTML).not.toContain(secret)
})
