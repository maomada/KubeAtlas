// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudInstall } from './CloudClusters'
import { ConnectionProfilesProvider } from './ConnectionProfiles'
import { setApiBase } from '../api/config'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  setApiBase('/c/previous-cluster/api')
})
afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  setApiBase('/api')
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function register() {
  await act(async () => root.render(<QueryClientProvider client={client}><ConnectionProfilesProvider><CloudInstall /></ConnectionProfilesProvider></QueryClientProvider>))
  const inputs = host.querySelectorAll('input')
  await act(async () => {
    for (const [index, value] of ['Demo east', 'demo-east', 'demo'].entries()) {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(inputs[index], value)
      inputs[index].dispatchEvent(new Event('input', { bubbles: true }))
    }
  })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
}

it('compiles the cluster ID pattern in Unicode sets mode and rejects invalid IDs before POST', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>((url) => {
    if (url !== '/api/hub/connections') throw new Error('unexpected request ' + url)
    return Promise.resolve(new Response(JSON.stringify({
      lanURL: 'https://lan.example.test', publicURL: '', ownerUserID: 'kubeatlas:portal:' + 'a'.repeat(64), catalogAvailable: true,
      catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: 'sha256:' + 'd'.repeat(64) },
    }), { headers: { 'Content-Type': 'application/json' } }))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<QueryClientProvider client={client}><ConnectionProfilesProvider><CloudInstall /></ConnectionProfilesProvider></QueryClientProvider>))
  const idInput = host.querySelector<HTMLInputElement>('input[pattern]')!
  const pattern = idInput.getAttribute('pattern')!
  const matcher = new RegExp('^(?:' + pattern + ')$', 'v')
  const setValue = (input: HTMLInputElement, value: string) => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  for (const id of ['a', 'alpha-1', 'a--b', 'a'.repeat(63)]) {
    expect(matcher.test(id)).toBe(true)
    await act(async () => setValue(idInput, id))
    expect(idInput.checkValidity()).toBe(true)
  }
  for (const id of ['A', '_a', '-a', 'a_b', 'a'.repeat(64)]) {
    expect(matcher.test(id)).toBe(false)
  }

  const nameInput = host.querySelector<HTMLInputElement>('input[placeholder="Production EU"]')!
  await act(async () => {
    setValue(nameInput, 'Demo east')
    setValue(idInput, 'A')
  })
  expect(idInput.checkValidity()).toBe(false)
  await act(async () => host.querySelector('form')!.requestSubmit())
  expect(fetch.mock.calls.some(([url, init]) => url === '/api/hub/clusters' && init?.method === 'POST')).toBe(false)
})

it('registers through the Hub and discards the one-time Agent token after dismissal', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>((url) => Promise.resolve(url === '/api/hub/connections'
    ? new Response(JSON.stringify({ lanURL: 'https://lan.example.test', publicURL: 'https://public.example.test', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: true, catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` } }), { headers: { 'Content-Type': 'application/json' } })
    : new Response(JSON.stringify({ cluster: { id: 'demo-east', name: 'Demo east' }, token: 'unit-test-agent-token' }), { status: 201, headers: { 'Content-Type': 'application/json' } })))
  vi.stubGlobal('fetch', fetch)
  await register()
  expect(fetch).toHaveBeenCalledTimes(2)
  const create = fetch.mock.calls.find(([url]) => url === '/api/hub/clusters')!
  const options = create[1] as RequestInit
  expect(JSON.parse(options.body as string)).toMatchObject({ id: 'demo-east', name: 'Demo east', environment: 'demo' })
  expect(document.querySelector<HTMLTextAreaElement>('textarea[readonly]')?.value).toBe('unit-test-agent-token')
  const command = document.querySelector('pre')?.textContent ?? ''
  expect(command.startsWith('(')).toBe(true)
  expect(command).toContain("--cluster-id 'demo-east'")
  expect(command).toContain("--write-out '%{http_code}'")
  expect(command).toContain('[ "$HTTP_STATUS" = 200 ]')
  expect(command).toContain('read -r -s -p')
  expect(command).not.toContain('unit-test-agent-token')
  const ca = [...document.querySelectorAll('input')].find(input => input.getAttribute('placeholder') === '/path/to/hub-ca.crt')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ca, "/tmp/private CA's.pem")
    ca.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const caCommand = document.querySelector('pre')?.textContent ?? ''
  expect(caCommand).toContain("CA_FILE='/tmp/private CA'\\''s.pem'")
  expect(caCommand).toContain('CURL_CA=(--cacert "$CA_FILE")')
  expect(caCommand).toContain('INSTALLER_CA=(--ca-file "$CA_FILE")')
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === '我已安全保存 Token')!.click())
  expect(document.querySelector('textarea[readonly]')).toBeNull()
  expect(host.textContent).toContain('Cluster demo-east: Agent registered')
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Check connection status')).toBe(true)
})

it('checks a registered cluster after dismissing its one-time token', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>((url, init) => {
    if (url === '/api/hub/connections') return Promise.resolve(new Response(JSON.stringify({
      lanURL: 'https://lan.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: true,
      catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` },
    }), { headers: { 'Content-Type': 'application/json' } }))
    if (url === '/api/hub/clusters' && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ cluster: { id: 'demo-east', name: 'Demo east' }, token: 'unit-test-agent-token' }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    if (url === '/api/hub/clusters') return Promise.resolve(new Response(JSON.stringify({ clusters: [{ id: 'demo-east', state: 'connected' }] }), { headers: { 'Content-Type': 'application/json' } }))
    throw new Error(`unexpected request ${url}`)
  })
  vi.stubGlobal('fetch', fetch)
  await register()
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === '我已安全保存 Token')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Check connection status')!.click())
  expect(host.textContent).toContain('Cluster demo-east: Connected')
  expect(fetch.mock.calls.some(([url, init]) => url === '/api/hub/clusters' && init?.method !== 'POST')).toBe(true)
  expect(document.querySelector('textarea[readonly]')).toBeNull()
})

it('reports polling failures as unknown and allows a successful retry after timeout', async () => {
  let failChecks = true
  const fetch = vi.fn<typeof globalThis.fetch>((url, init) => {
    if (url === '/api/hub/connections') return Promise.resolve(new Response(JSON.stringify({
      lanURL: 'https://lan.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: true,
      catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` },
    }), { headers: { 'Content-Type': 'application/json' } }))
    if (url === '/api/hub/clusters' && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ cluster: { id: 'demo-east', name: 'Demo east' }, token: 'unit-test-agent-token' }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    if (url === '/api/hub/clusters' && failChecks) return new Promise<Response>(() => {})
    if (url === '/api/hub/clusters') return Promise.resolve(new Response(JSON.stringify({ clusters: [{ id: 'demo-east', state: 'connected' }] }), { headers: { 'Content-Type': 'application/json' } }))
    throw new Error(`unexpected request ${url}`)
  })
  vi.stubGlobal('fetch', fetch)
  await register()
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === '我已安全保存 Token')!.click())
  vi.useFakeTimers({ now: 0 })
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Check connection status')!.click())
  await act(async () => { await vi.advanceTimersByTimeAsync(120000) })
  expect(host.textContent).toContain('status could not be checked')
  expect(host.textContent).toContain('unknown')
  expect(host.textContent).not.toContain('offline')
  failChecks = false
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Retry status check')!.click())
  expect(host.textContent).toContain('Cluster demo-east: Connected')
})

it('ignores a delayed status response for a previously registered cluster', async () => {
  let resolveOld!: (response: Response) => void
  const oldStatus = new Promise<Response>(resolve => { resolveOld = resolve })
  let statusRequests = 0
  const fetch = vi.fn<typeof globalThis.fetch>((url, init) => {
    if (url === '/api/hub/connections') return Promise.resolve(new Response(JSON.stringify({
      lanURL: 'https://lan.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: true,
      catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` },
    }), { headers: { 'Content-Type': 'application/json' } }))
    if (url === '/api/hub/clusters' && init?.method === 'POST') {
      const id = JSON.parse(init.body as string).id as string
      return Promise.resolve(new Response(JSON.stringify({ cluster: { id, name: id }, token: `${id}-token` }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    }
    if (url === '/api/hub/clusters' && statusRequests++ === 0) return oldStatus
    throw new Error(`unexpected request ${url}`)
  })
  vi.stubGlobal('fetch', fetch)
  await register()
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === '我已安全保存 Token')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Check connection status')!.click())
  const inputs = host.querySelectorAll('input')
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(inputs[0], 'Demo west')
    inputs[0].dispatchEvent(new Event('input', { bubbles: true }))
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(inputs[1], 'demo-west')
    inputs[1].dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => resolveOld(new Response(JSON.stringify({ clusters: [{ id: 'demo-east', state: 'connected' }] }), { headers: { 'Content-Type': 'application/json' } })))
  expect(host.textContent).toContain('Cluster demo-west: Agent registered')
  expect(host.textContent).not.toContain('Cluster demo-west: Connected')
})

it('shows uncertain registration failure and never retries the mutation automatically', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>((url) => url === '/api/hub/connections'
    ? Promise.resolve(new Response(JSON.stringify({ lanURL: 'https://lan.example.test', publicURL: '', ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable: true, catalog: { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` } }), { headers: { 'Content-Type': 'application/json' } }))
    : Promise.reject(new TypeError('Network connection lost')))
  vi.stubGlobal('fetch', fetch)
  await register()
  expect(fetch.mock.calls.filter(([url]) => url === '/api/hub/clusters')).toHaveLength(1)
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Network connection lost')
  expect(document.querySelector('textarea[readonly]')).toBeNull()
})
