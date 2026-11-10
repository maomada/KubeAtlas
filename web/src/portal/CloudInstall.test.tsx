// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudInstall } from './CloudClusters'
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
  vi.unstubAllGlobals()
})

async function register() {
  await act(async () => root.render(<QueryClientProvider client={client}><CloudInstall /></QueryClientProvider>))
  const inputs = host.querySelectorAll('input')
  await act(async () => {
    for (const [index, value] of ['Demo east', 'demo-east', 'demo'].entries()) {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(inputs[index], value)
      inputs[index].dispatchEvent(new Event('input', { bubbles: true }))
    }
  })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
}

it('registers through the Hub and discards the one-time Agent token after dismissal', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(() => Promise.resolve(new Response(JSON.stringify({ cluster: { id: 'demo-east', name: 'Demo east' }, token: 'unit-test-agent-token' }), { status: 201, headers: { 'Content-Type': 'application/json' } })))
  vi.stubGlobal('fetch', fetch)
  await register()
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0][0]).toBe('/api/hub/clusters')
  const options = fetch.mock.calls[0][1] as RequestInit
  expect(JSON.parse(options.body as string)).toMatchObject({ id: 'demo-east', name: 'Demo east', environment: 'demo' })
  expect(document.querySelector<HTMLTextAreaElement>('textarea[readonly]')?.value).toBe('unit-test-agent-token')
  expect(document.querySelector('pre')?.textContent).toContain("KUBEATLAS_AGENT_HUB_CLUSTER_ID='demo-east'")
  expect(document.querySelector('pre')?.textContent).not.toContain('unit-test-agent-token')
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === 'I saved the token')!.click())
  expect(document.querySelector('textarea[readonly]')).toBeNull()
})

it('shows uncertain registration failure and never retries the mutation automatically', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(() => Promise.reject(new TypeError('Network connection lost')))
  vi.stubGlobal('fetch', fetch)
  await register()
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Network connection lost')
  expect(document.querySelector('textarea[readonly]')).toBeNull()
})
