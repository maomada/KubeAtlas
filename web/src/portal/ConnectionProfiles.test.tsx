// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ConnectionProfileSelector, ConnectionProfilesProvider, ConnectionSettingsEditor, useConnectionProfiles } from './ConnectionProfiles'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const data = (lanURL: string, publicURL: string, catalogAvailable = true) => ({
  lanURL, publicURL, ownerUserID: `kubeatlas:portal:${'a'.repeat(64)}`, catalogAvailable,
  catalog: catalogAvailable ? { available: true, version: '1.2.3', installerSHA256: 'b'.repeat(64), chartSHA256: 'c'.repeat(64), imageDigest: `sha256:${'d'.repeat(64)}` } : { available: false },
})
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>

function Probe() {
  const profile = useConnectionProfiles()
  return <><output aria-label="selected profile">{JSON.stringify({ profile: profile.profile, origin: profile.origin, mcpURL: profile.mcpURL, installAvailable: profile.installAvailable, loading: profile.loading, error: profile.error })}</output><button onClick={() => { void profile.refresh() }}>Refresh profile</button></>
}

beforeEach(() => {
  window.localStorage.removeItem('kubeatlas.hub.connection-profile')
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

it('switches the shared MCP and onboarding origin and stores only the profile choice', async () => {
  const fetch = vi.fn(() => Promise.resolve(response(data('https://lan.example.test', 'https://public.example.test'))))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><ConnectionProfileSelector /><Probe /></ConnectionProfilesProvider>))
  expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ profile: 'lan', origin: 'https://lan.example.test', mcpURL: 'https://lan.example.test/mcp', installAvailable: true })
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '公网')!.click())
  expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ profile: 'public', origin: 'https://public.example.test', mcpURL: 'https://public.example.test/mcp' })
  expect(window.localStorage.getItem('kubeatlas.hub.connection-profile')).toBe('public')
  expect(window.localStorage.length).toBe(1)
  expect(window.localStorage.key(0)).toBe('kubeatlas.hub.connection-profile')
})

it('keeps empty profiles unavailable rather than falling back to the browser origin', async () => {
  const fetch = vi.fn(() => Promise.resolve(response(data('', '', false))))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><ConnectionProfileSelector /><Probe /></ConnectionProfilesProvider>))
  expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ origin: '', mcpURL: '', installAvailable: false })
  expect(host.textContent).not.toContain(window.location.origin)
})

it('clears stale installation data when a later profile refresh fails', async () => {
  let refreshFails = false
  const fetch = vi.fn(() => Promise.resolve(refreshFails ? Promise.reject(new TypeError('connection settings unavailable')) : response(data('https://lan.example.test', 'https://public.example.test'))))
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><ConnectionProfileSelector /><ConnectionSettingsEditor canManage /><Probe /></ConnectionProfilesProvider>))
  expect(JSON.parse(host.querySelector('output')!.textContent!).installAvailable).toBe(true)
  refreshFails = true
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Refresh profile')!.click())
  expect(JSON.parse(host.querySelector('output')!.textContent!)).toMatchObject({ origin: '', installAvailable: false, loading: false, error: 'connection settings unavailable' })
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('connection settings unavailable')
  expect(host.querySelector<HTMLInputElement>('input[placeholder="https://192.0.2.10"]')?.disabled).toBe(true)
})

it('surfaces read and update errors and permits an explicitly empty address', async () => {
  let failUpdate = true
  const fetch = vi.fn((url: string, options?: RequestInit) => {
    if (url === '/api/hub/connections' && options?.method === 'PUT') return Promise.resolve(failUpdate ? response({ message: 'invalid origin' }, 400) : response(data('', 'https://public.example.test', false)))
    return Promise.resolve(response(data('https://lan.example.test', '', false)))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<ConnectionProfilesProvider><ConnectionSettingsEditor canManage /><Probe /></ConnectionProfilesProvider>))
  const lan = host.querySelector<HTMLInputElement>('input[placeholder="https://192.0.2.10"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(lan, '')
    lan.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('invalid origin')
  failUpdate = false
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(fetch.mock.calls.some(([url, options]) => url === '/api/hub/connections' && options?.method === 'PUT' && JSON.parse(options.body as string).lanURL === '')).toBe(true)
  expect(host.textContent).toContain('连接地址已保存')
})
