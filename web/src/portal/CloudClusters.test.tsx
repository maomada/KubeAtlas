// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudClusters } from './CloudClusters'
import { useCloudQuery, type HubCluster } from './cloudApi'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const connected: HubCluster = {
  id: 'connected', organizationId: 'org-1', name: 'Active', description: '', environment: '', region: '',
  createdAt: '', updatedAt: '', configured: true, state: 'connected',
}
const revoked: HubCluster = { ...connected, id: 'revoked', name: 'Retired', state: 'revoked' }

let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  vi.unstubAllGlobals()
})

it('requires confirmation and removes only the revoked row after the server accepts deletion', async () => {
  let clusters = [connected, revoked]
  let allowDelete = false
  const directoryKey = ['portal', '/api', '/hub/clusters']
  client.setQueryData(directoryKey, { clusters })
  const request = vi.fn<typeof fetch>(async (path, init) => {
    if (init?.method === 'GET') return Response.json({ clusters })
    expect(path).toBe('/api/hub/clusters/revoked')
    expect(init?.method).toBe('DELETE')
    if (!allowDelete) return Response.json({ message: '集群状态已变化' }, { status: 409 })
    clusters = [connected]
    return new Response(null, { status: 204 })
  })
  vi.stubGlobal('fetch', request)

  function Directory({ canManage }: { canManage: boolean }) {
    const directory = useCloudQuery<{ clusters: HubCluster[] }>('/hub/clusters')
    return <CloudClusters clusters={directory.data?.clusters ?? []} canManage={canManage} />
  }
  const render = (canManage: boolean) => act(async () => root.render(<QueryClientProvider client={client}><Directory canManage={canManage} /></QueryClientProvider>))
  const deleteButtons = () => host.querySelectorAll<HTMLButtonElement>('button[aria-label^="删除集群："]')
  const deletionCalls = () => request.mock.calls.filter(([, init]) => init?.method === 'DELETE')

  await render(false)
  expect(deleteButtons()).toHaveLength(0)
  await render(true)
  expect(deleteButtons()).toHaveLength(1)
  const deleteButton = deleteButtons()[0]
  expect(deleteButton.getAttribute('aria-label')).toBe('删除集群：Retired')
  deleteButton.focus()
  await act(async () => deleteButton.click())

  const cancel = document.activeElement as HTMLButtonElement
  expect(cancel.textContent).toBe('取消')
  await act(async () => cancel.click())
  expect(deletionCalls()).toHaveLength(0)
  expect(document.activeElement).toBe(deleteButton)
  await act(async () => deleteButton.click())

  const dialog = document.querySelector('[role="dialog"]')!
  const form = dialog.querySelector('form')!
  const confirm = dialog.querySelector<HTMLButtonElement>('button[type="submit"]')!
  expect(document.activeElement?.textContent).toBe('取消')
  expect(confirm.disabled).toBe(true)
  await act(async () => form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(deletionCalls()).toHaveLength(0)

  const input = form.querySelector('input')!
  const typeName = (name: string) => act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, name)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await typeName('retired')
  expect(confirm.disabled).toBe(true)
  await typeName('Retired')
  await act(async () => confirm.click())
  expect(dialog.querySelector('[role="alert"]')?.textContent).toContain('集群状态已变化')
  expect(host.textContent).toContain('Retired')

  allowDelete = true
  await act(async () => confirm.click())
  await vi.waitFor(() => expect(host.textContent).not.toContain('Retired'))
  expect(host.textContent).toContain('Active')
  expect(document.activeElement).toBe(host.querySelector('h1'))
  expect(deletionCalls()).toHaveLength(2)
})
