// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Register } from './Registration'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

it('submits email and password once, reports pending, and does not create a session', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = String(input)
    if (path === '/api/auth/providers') return response({ registrationEnabled: true, oidc: { enabled: false } })
    if (path === '/api/auth/register' && init?.method === 'POST') return response({ status: 'pending' }, 201)
    throw new Error('Unexpected request: ' + path)
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Register />))
  await act(async () => vi.waitFor(() => expect(host.querySelector('form')).not.toBeNull()))
  const email = host.querySelector<HTMLInputElement>('input[type="email"]')!
  const password = host.querySelector<HTMLInputElement>('input[type="password"]')!
  await act(async () => setValue(email, 'person@example.test'))
  await act(async () => setValue(password, 'strong-password-123'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('注册申请已提交')))
  const registration = fetch.mock.calls.find(([url]) => String(url) === '/api/auth/register')
  expect(registration?.[1]?.method).toBe('POST')
  expect(JSON.parse(registration?.[1]?.body as string)).toEqual({ email: 'person@example.test', password: 'strong-password-123' })
  expect(fetch.mock.calls.some(([url]) => String(url) === '/api/auth/session' || String(url) === '/api/auth/login')).toBe(false)
  expect(host.querySelector('input[type="password"]')).toBeNull()
  expect(host.querySelector('a[href="/login"]')).not.toBeNull()
})

it('fails closed when registration is disabled or provider configuration is unavailable', async () => {
  for (const providerResponse of [
    response({ registrationEnabled: false }, 200),
    response({ message: 'provider unavailable' }, 503),
  ]) {
    const fetch = vi.fn<typeof globalThis.fetch>(async input => {
      if (String(input) === '/api/auth/providers') return providerResponse
      throw new Error('Unexpected request: ' + String(input))
    })
    vi.stubGlobal('fetch', fetch)
    await act(async () => root.render(<Register key={providerResponse.status} />))
    const expected = providerResponse.status === 200 ? '未开放公开注册' : 'Authentication options are unavailable'
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain(expected)))
    expect(host.querySelector('form')).toBeNull()
    expect(fetch.mock.calls.some(([url]) => String(url) === '/api/auth/register')).toBe(false)
  }
})

it('rejects passwords over the bcrypt UTF-8 byte bound before posting', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/auth/providers') return response({ registrationEnabled: true })
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Register />))
  await act(async () => vi.waitFor(() => expect(host.querySelector('form')).not.toBeNull()))
  const email = host.querySelector<HTMLInputElement>('input[type="email"]')!
  const password = host.querySelector<HTMLInputElement>('input[type="password"]')!
  await act(async () => setValue(email, 'person@example.test'))
  await act(async () => setValue(password, 'é'.repeat(37)))
  expect(new TextEncoder().encode(password.value).length).toBe(74)
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('UTF-8 编码超过 72 字节')
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true)
  await act(async () => setValue(password, '😀'.repeat(11)))
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true)
  await act(async () => setValue(password, '😀'.repeat(12)))
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(false)
  await act(async () => setValue(password, 'é'.repeat(36)))
  expect(new TextEncoder().encode(password.value).length).toBe(72)
  expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(false)
  expect(fetch.mock.calls.some(([url]) => String(url) === '/api/auth/register')).toBe(false)
})

it('clears a failed registration password and shows structured server details', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    if (String(input) === '/api/auth/providers') return response({ registrationEnabled: true })
    if (String(input) === '/api/auth/register') return response({ error_code: 'registration_closed', message: 'Registration is unavailable', requestId: 'req-42' }, 503)
    throw new Error('Unexpected request: ' + String(input))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<Register />))
  await act(async () => vi.waitFor(() => expect(host.querySelector('form')).not.toBeNull()))
  await act(async () => setValue(host.querySelector<HTMLInputElement>('input[type="email"]')!, 'person@example.test'))
  const password = host.querySelector<HTMLInputElement>('input[type="password"]')!
  await act(async () => setValue(password, 'strong-password-123'))
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('req-42')))
  expect(host.textContent).toContain('registration_closed')
  expect(host.textContent).toContain('Registration is unavailable')
  expect(host.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('')
})
