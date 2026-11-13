// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CloudPlatform } from './CloudPlatform'
import { setApiBase } from '../api/config'
import type { HubCluster } from './cloudApi'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient
const cluster: HubCluster = {
  id: 'cluster-1', organizationId: 'org-1', name: 'East cluster', description: '', environment: '',
  region: '', createdAt: '', updatedAt: '', configured: true, state: 'connected',
}
const users = [
  { id: 'local', email: 'local@example.test', displayName: 'Local recovery account', status: 'active', local: true, platformOwner: true, memberships: [] },
  { id: 'pending-user', email: 'pending@example.test', displayName: 'Pending user', status: 'pending', local: false, platformOwner: false, memberships: [] },
  { id: 'active-user', email: 'active@example.test', displayName: 'Active user', status: 'active', local: false, platformOwner: false, memberships: [{ organizationId: 'org-1', role: 'owner' }, { organizationId: 'org-2', role: 'member' }] },
]
const organizations = [
  { id: 'org-1', name: 'Organization One', role: 'owner' },
  { id: 'org-2', name: 'Organization Two', role: 'member' },
  { id: 'org-3', name: 'Organization Three', role: 'viewer' },
]
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const setValue = (input: HTMLInputElement | HTMLSelectElement, value: string) => {
  const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  setApiBase('/api')
})
afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  setApiBase('/api')
  vi.unstubAllGlobals()
})
async function renderPlatform(platformOwner = true) {
  await act(async () => root.render(<QueryClientProvider client={client}><CloudPlatform platformOwner={platformOwner} /></QueryClientProvider>))
}
function stubDirectory(mutation: (path: string, init?: RequestInit) => Response | Promise<Response>, readUsers = () => users, readOrganizations = () => organizations, readClusters = () => [cluster]) {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = String(input)
    if (init?.method === 'GET' && path === '/api/hub/platform/users') return response({ users: readUsers() })
    if (init?.method === 'GET' && path === '/api/hub/organizations') return response({ organizations: readOrganizations() })
    if (init?.method === 'GET' && path === '/api/hub/platform/clusters') return response({ clusters: readClusters() })
    return mutation(path, init)
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

it('does not fetch platform endpoints for an organization owner without platformOwner', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  await renderPlatform(false)
  expect(host.textContent).toContain('明确 platformOwner 权限')
  expect(fetch).not.toHaveBeenCalled()
})

it('requires organization and role before pending-account approval and sends the atomic user body', async () => {
  const fetch = stubDirectory(path => {
    if (path === '/api/hub/platform/users/pending-user' ) return response({ ...users[1], status: 'active', memberships: [{ organizationId: 'org-1', role: 'viewer' }] })
    throw new Error('Unexpected request: ' + path)
  })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Pending user')))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '审核申请')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('select')!, 'active'))
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('[aria-label="要添加的组织"]')!, 'org-1'))
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('[aria-label="新成员角色"]')!, 'viewer'))
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '添加成员资格')!.click())
  const save = [...dialog.querySelectorAll('button')].find(button => button.textContent === '保存账号')!
  expect(save.disabled).toBe(true)
  expect(dialog.textContent).toContain('最后一位组织 Owner 由服务器保护')
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  expect(save.disabled).toBe(false)
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(request?.[0]).toBe('/api/hub/platform/users/pending-user')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ status: 'active', memberships: [{ organizationId: 'org-1', role: 'viewer' }] })
})

it('preserves every existing membership unless explicitly removed', async () => {
  const fetch = stubDirectory(path => {
    if (path === '/api/hub/platform/users/active-user') return response(users[2])
    throw new Error('Unexpected request: ' + path)
  })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Local recovery account')))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '编辑成员资格')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('[aria-label="组织角色：Organization One"]')!, 'viewer'))
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({
    status: 'active',
    memberships: [{ organizationId: 'org-1', role: 'viewer' }, { organizationId: 'org-2', role: 'member' }],
  })
})

it('creates an organization with an active initial owner and refreshes organization data', async () => {
  let currentOrganizations = organizations
  const created = { id: 'org-4', name: 'New organization', role: 'owner' as const }
  const fetch = stubDirectory((path, init) => {
    if (path === '/api/hub/organizations' && init?.method === 'POST') {
      currentOrganizations = [...currentOrganizations, created]
      return response(created, 201)
    }
    throw new Error('Unexpected request: ' + path)
  }, () => users, () => currentOrganizations)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const localRow = [...host.querySelectorAll('.cloud-platform-user')].find(row => row.textContent?.includes('Local recovery account'))!
  expect(localRow.textContent).toContain('平台 Owner')
  expect(localRow.querySelector('button')?.disabled).toBe(true)
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '创建组织')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  await act(async () => setValue(dialog.querySelector<HTMLInputElement>('input')!, 'New organization'))
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('select')!, 'local'))
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([url, init]) => String(url) === '/api/hub/organizations' && init?.method === 'POST')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ name: 'New organization', ownerMemberId: 'local' })
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('New organization')))
  expect(fetch.mock.calls.some(([url, init]) => String(url) === '/api/hub/organizations' && init?.method === 'GET')).toBe(true)
})

it('keeps organization-creation failures inside the dialog', async () => {
  const fetch = stubDirectory((path, init) => {
    if (path === '/api/hub/organizations' && init?.method === 'POST') return response({ message: 'Organization creation failed' }, 500)
    throw new Error('Unexpected request: ' + path)
  })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('创建组织')))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '创建组织')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  await act(async () => setValue(dialog.querySelector<HTMLInputElement>('input')!, 'New organization'))
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('select')!, 'local'))
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(dialog.textContent).toContain('Organization creation failed')))
  expect(document.querySelector('[role="dialog"]')).not.toBeNull()
  expect(fetch.mock.calls.some(([url, init]) => String(url) === '/api/hub/organizations' && init?.method === 'POST')).toBe(true)
})

it('transfers any cluster to a selected third organization using the frozen response envelope', async () => {
  let currentCluster = cluster
  const fetch = stubDirectory((path, init) => {
    if (path === '/api/hub/clusters/cluster-1/transfer' && init?.method === 'POST') {
      currentCluster = { ...cluster, organizationId: 'org-3' }
      return response({ cluster: currentCluster })
    }
    throw new Error('Unexpected request: ' + path)
  }, () => users, () => organizations, () => [currentCluster])
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('East cluster')))
  const clusterArticle = [...host.querySelectorAll('.cloud-platform-cluster')].find(item => item.textContent?.includes('East cluster'))!
  await act(async () => [...clusterArticle.querySelectorAll('button')].find(button => button.textContent === '转移')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Organization One')
  expect(dialog.textContent).toContain('远端 Secret 保持不变')
  const target = dialog.querySelector<HTMLSelectElement>('[aria-label="转移目标组织"]')!
  expect([...target.options].map(option => option.value)).toContain('org-3')
  await act(async () => setValue(target, 'org-3'))
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '确认转移')!.click())
  const request = fetch.mock.calls.find(([url, init]) => String(url) === '/api/hub/clusters/cluster-1/transfer' && init?.method === 'POST')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ organizationId: 'org-3' })
  await act(async () => vi.waitFor(() => expect(host.querySelector('.cloud-platform-cluster')?.textContent).toContain('Organization Three')))
  expect(fetch.mock.calls.filter(([url, init]) => String(url) === '/api/hub/platform/clusters' && init?.method === 'GET').length).toBeGreaterThanOrEqual(2)
})
