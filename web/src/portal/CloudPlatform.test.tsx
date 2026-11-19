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

function groupName(section: HTMLElement): string {
  return section.getAttribute('aria-label') ?? (section.getAttribute('aria-labelledby') ?? '').split(/\s+/)
    .map(id => document.getElementById(id)?.textContent ?? '').join(' ').trim()
}

function namedGroup(name: string, within: HTMLElement = host): HTMLElement {
  const found = [...within.querySelectorAll<HTMLElement>('[role="group"]')].find(section => groupName(section) === name)
  expect(found, `Missing named group: ${name}`).toBeDefined()
  return found!
}

function groupForId(id: string, within: HTMLElement): HTMLElement {
  const found = [...within.querySelectorAll<HTMLElement>('[role="group"]')].find(section => groupName(section).includes(id))
  expect(found, `Missing organization group with ID: ${id}`).toBeDefined()
  return found!
}

function groupCount(group: HTMLElement): number {
  const heading = group.querySelector('h3')!
  expect(heading).not.toBeNull()
  const count = [...heading.parentElement!.querySelectorAll('span')].map(span => span.textContent?.trim() ?? '')
    .find(text => /^\d+\s*(?:人|位成员|个用户|个账号|个集群)?$/.test(text))
  expect(count, 'Missing visible group count').toBeDefined()
  return Number.parseInt(count!, 10)
}

function userRow(group: HTMLElement, displayName: string): HTMLElement {
  const found = [...group.querySelectorAll<HTMLElement>('.cloud-platform-user')].find(row => row.textContent?.includes(displayName))
  expect(found, `Missing user row: ${displayName}`).toBeDefined()
  return found!
}

function roleLabels(row: HTMLElement): string[] {
  return [...row.querySelectorAll('span')].filter(span => !span.childElementCount)
    .map(span => span.textContent?.trim() ?? '').filter(text => ['组织 Owner', '成员', '只读成员'].includes(text))
}

it('does not fetch platform endpoints for an organization owner without platformOwner', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  await renderPlatform(false)
  expect(host.textContent).toContain('明确 platformOwner 权限')
  expect(fetch).not.toHaveBeenCalled()
})

it('requires organization and role before pending-account approval and sends the atomic user body', async () => {
  let currentUsers = users
  const fetch = stubDirectory(path => {
    if (path === '/api/hub/platform/users/pending-user' ) {
      const updated = { ...users[1], status: 'active', memberships: [{ organizationId: 'org-1', role: 'viewer' }] }
      currentUsers = currentUsers.map(user => user.id === updated.id ? updated : user)
      return response(updated)
    }
    throw new Error('Unexpected request: ' + path)
  }, () => currentUsers)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Pending user')))
  const opener = userRow(namedGroup('待审批', namedGroup('平台用户')), 'Pending user').querySelector('button')!
  await act(async () => { opener.focus(); opener.click() })
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
  await act(async () => vi.waitFor(() => {
    const replacement = userRow(namedGroup('Organization One', namedGroup('平台用户')), 'Pending user').querySelector('button')!
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(opener.isConnected).toBe(false)
    expect(document.activeElement).toBe(replacement)
  }))
})

it('preserves every existing membership unless explicitly removed', async () => {
  const fetch = stubDirectory(path => {
    if (path === '/api/hub/platform/users/active-user') return response(users[2])
    throw new Error('Unexpected request: ' + path)
  })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Local recovery account')))
  const secondOrganizationRow = userRow(namedGroup('Organization Two', namedGroup('平台用户')), 'Active user')
  const opener = secondOrganizationRow.querySelector('button')!
  await act(async () => { opener.focus(); opener.click() })
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.querySelectorAll('.cloud-platform-membership-edit select[aria-label^="组织角色："]')).toHaveLength(2)
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('[aria-label="组织角色：Organization Two"]')!, 'viewer'))
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({
    status: 'active',
    memberships: [{ organizationId: 'org-1', role: 'owner' }, { organizationId: 'org-2', role: 'viewer' }],
  })
  await act(async () => vi.waitFor(() => {
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(opener)
  }))
})

it('returns account focus to its remaining organization after removing the opening membership', async () => {
  let currentUsers = users
  stubDirectory((path, init) => {
    if (path === '/api/hub/platform/users/active-user' && init?.method === 'PUT') {
      const updated = { ...users[2], memberships: [{ organizationId: 'org-1', role: 'owner' }] }
      currentUsers = currentUsers.map(user => user.id === updated.id ? updated : user)
      return response(updated)
    }
    throw new Error('Unexpected request: ' + path)
  }, () => currentUsers)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const userGroups = namedGroup('平台用户')
  const opener = userRow(namedGroup('Organization Two', userGroups), 'Active user').querySelector('button')!
  await act(async () => { opener.focus(); opener.click() })
  const dialog = document.querySelector('[role="dialog"]')!
  const membership = [...dialog.querySelectorAll('.cloud-platform-membership-edit')]
    .find(row => row.querySelector('strong')?.textContent === 'Organization Two')!
  await act(async () => membership.querySelector('button')!.click())
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => {
    const replacement = userRow(namedGroup('Organization One', userGroups), 'Active user').querySelector('button')!
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(opener.isConnected).toBe(false)
    expect(namedGroup('Organization Two', userGroups).querySelectorAll('.cloud-platform-user')).toHaveLength(0)
    expect(document.activeElement).toBe(replacement)
  }))
})

it('returns focus to the users heading if the saved account is no longer in the refreshed directory', async () => {
  let currentUsers = users
  stubDirectory((path, init) => {
    if (path === '/api/hub/platform/users/active-user' && init?.method === 'PUT') {
      currentUsers = currentUsers.filter(user => user.id !== 'active-user')
      return response(users[2])
    }
    throw new Error('Unexpected request: ' + path)
  }, () => currentUsers)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const userGroups = namedGroup('平台用户')
  const opener = userRow(namedGroup('Organization Two', userGroups), 'Active user').querySelector('button')!
  await act(async () => { opener.focus(); opener.click() })
  const dialog = document.querySelector('[role="dialog"]')!
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => {
    const heading = userGroups.querySelector('h2')!
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(opener.isConnected).toBe(false)
    expect(document.activeElement).toBe(heading)
    expect(heading.tabIndex).toBe(-1)
  }))
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
    if (path === '/api/hub/organizations' && init?.method === 'POST') return response({ error_code: 'organization_creation_failed', message: 'Organization creation failed', requestId: 'org-create-42' }, 500)
    throw new Error('Unexpected request: ' + path)
  })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const createOrganization = () => [...host.querySelectorAll('button')].find(button => button.textContent === '创建组织')!
  await act(async () => vi.waitFor(() => expect(createOrganization().disabled).toBe(false)))
  await act(async () => createOrganization().click())
  await act(async () => vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull()))
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!
  await act(async () => setValue(dialog.querySelector<HTMLInputElement>('input')!, 'New organization'))
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('select')!, 'local'))
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  await act(async () => vi.waitFor(() => expect(dialog.textContent).toContain('Organization creation failed')))
  expect(dialog.textContent).toContain('错误代码：organization_creation_failed')
  expect(dialog.textContent).toContain('请求 ID：org-create-42')
  expect(document.querySelector('[role="dialog"]')).toBe(dialog)
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
  const clusterGroups = namedGroup('集群归属')
  expect(groupCount(namedGroup('Organization One', clusterGroups))).toBe(1)
  expect(groupCount(namedGroup('Organization Three', clusterGroups))).toBe(0)
  const clusterArticle = [...host.querySelectorAll('.cloud-platform-cluster')].find(item => item.textContent?.includes('East cluster'))!
  await act(async () => [...clusterArticle.querySelectorAll('button')].find(button => button.textContent === '转移')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Organization One')
  expect(dialog.textContent).toContain('Agent 会自动重连，无需重新配对')
  expect(dialog.textContent).toContain('原组织的访问权限和相关 MCP 凭据将失效')
  expect(dialog.textContent).not.toContain('必须明确批准重新配对')
  const target = dialog.querySelector<HTMLSelectElement>('[aria-label="转移目标组织"]')!
  expect([...target.options].map(option => option.value)).toContain('org-3')
  await act(async () => setValue(target, 'org-3'))
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '确认转移')!.click())
  const request = fetch.mock.calls.find(([url, init]) => String(url) === '/api/hub/clusters/cluster-1/transfer' && init?.method === 'POST')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ organizationId: 'org-3' })
  await act(async () => vi.waitFor(() => expect(namedGroup('Organization Three', clusterGroups).querySelector('.cloud-platform-cluster')?.textContent).toContain('East cluster')))
  expect(namedGroup('Organization One', clusterGroups).querySelectorAll('.cloud-platform-cluster')).toHaveLength(0)
  expect(groupCount(namedGroup('Organization One', clusterGroups))).toBe(0)
  expect(groupCount(namedGroup('Organization Three', clusterGroups))).toBe(1)
  const targetButton = namedGroup('Organization Three', clusterGroups).querySelector<HTMLButtonElement>('.cloud-platform-cluster button')!
  await act(async () => vi.waitFor(() => {
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(targetButton)
  }))
  expect(fetch.mock.calls.filter(([url, init]) => String(url) === '/api/hub/platform/clusters' && init?.method === 'GET').length).toBeGreaterThanOrEqual(2)
})

it('keeps a previously revoked cluster revoked when only its organization changes', async () => {
  const revoked = { ...cluster, state: 'revoked' as const, revokedAt: '2026-01-01T00:00:00Z' }
  let currentCluster = revoked
  const fetch = stubDirectory((path, init) => {
    if (path === '/api/hub/clusters/cluster-1/transfer' && init?.method === 'POST') {
      currentCluster = { ...revoked, organizationId: 'org-3' }
      return response({ cluster: currentCluster })
    }
    throw new Error('Unexpected request: ' + path)
  }, () => users, () => organizations, () => [currentCluster])
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('East cluster')))
  const article = host.querySelector('.cloud-platform-cluster')!
  await act(async () => [...article.querySelectorAll('button')].find(button => button.textContent === '转移')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('此集群已撤销，本次仅变更归属')
  expect(dialog.textContent).not.toContain('Agent 会自动重连')
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('[aria-label="转移目标组织"]')!, 'org-3'))
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '确认转移')!.click())
  const clusterGroups = namedGroup('集群归属')
  await act(async () => vi.waitFor(() => expect(namedGroup('Organization Three', clusterGroups).querySelector('.cloud-platform-cluster')?.textContent).toContain('East cluster')))
  expect(namedGroup('Organization Three', clusterGroups).querySelector('.cloud-platform-cluster')?.textContent).toContain('已撤销')
  const posts = fetch.mock.calls.filter(([, init]) => init?.method === 'POST')
  expect(posts).toHaveLength(1)
  expect(posts[0][0]).toBe('/api/hub/clusters/cluster-1/transfer')
  expect(JSON.parse(posts[0][1]!.body as string)).toEqual({ organizationId: 'org-3' })
})

it('groups each ordinary membership by its organization role and retains empty directory groups', async () => {
  stubDirectory(path => { throw new Error('Unexpected mutation: ' + path) })
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const userGroups = namedGroup('平台用户')
  const first = namedGroup('Organization One', userGroups)
  const second = namedGroup('Organization Two', userGroups)
  const empty = namedGroup('Organization Three', userGroups)
  expect(first.querySelectorAll('.cloud-platform-user')).toHaveLength(1)
  expect(second.querySelectorAll('.cloud-platform-user')).toHaveLength(1)
  expect(first.querySelector('ul[role="list"] > li.cloud-platform-user')).not.toBeNull()
  expect(roleLabels(userRow(first, 'Active user'))).toEqual(['组织 Owner'])
  expect(roleLabels(userRow(second, 'Active user'))).toEqual(['成员'])
  expect(groupCount(first)).toBe(1)
  expect(groupCount(second)).toBe(1)
  expect(groupCount(empty)).toBe(0)
  expect(empty.querySelectorAll('.cloud-platform-user')).toHaveLength(0)
  const knownOrder = [...userGroups.querySelectorAll<HTMLElement>('[role="group"]')].map(groupName)
    .filter(name => organizations.some(organization => organization.name === name))
  expect(knownOrder).toEqual(organizations.map(organization => organization.name))
  const clusterGroups = namedGroup('集群归属')
  expect(groupCount(namedGroup('Organization One', clusterGroups))).toBe(1)
  for (const name of ['Organization Two', 'Organization Three']) {
    const group = namedGroup(name, clusterGroups)
    expect(groupCount(group)).toBe(0)
    expect(group.querySelectorAll('.cloud-platform-cluster')).toHaveLength(0)
  }
})

it('separates system identities and unassigned states without hiding pending or disabled memberships', async () => {
  const pendingMember = { ...users[2], id: 'pending-member', email: 'pending-member@example.test', displayName: 'Pending with memberships', status: 'pending' }
  const disabledMember = { ...users[2], id: 'disabled-member', email: 'disabled-member@example.test', displayName: 'Disabled with membership', status: 'disabled', memberships: [{ organizationId: 'org-2', role: 'viewer' }] }
  const admins = [
    { ...users[0], memberships: [{ organizationId: 'org-1', role: 'owner' }] },
    { ...users[0], id: 'local-only', displayName: 'Local only identity', platformOwner: false, memberships: [{ organizationId: 'org-2', role: 'owner' }] },
    { ...users[0], id: 'platform-only', displayName: 'Platform only identity', local: false, memberships: [{ organizationId: 'org-1', role: 'member' }] },
  ]
  let currentUsers = [
    ...admins, users[1], { ...users[2], displayName: 'admin' }, pendingMember, disabledMember,
    { ...users[2], id: 'active-unassigned', displayName: 'Active without memberships', memberships: [] },
    { ...users[2], id: 'disabled-unassigned', displayName: 'Disabled without memberships', status: 'disabled', memberships: [] },
  ]
  const fetch = stubDirectory((path, init) => {
    if (path === '/api/hub/platform/users/pending-member' && init?.method === 'PUT') {
      const updated = { ...pendingMember, status: 'active' }
      currentUsers = currentUsers.map(user => user.id === updated.id ? updated : user)
      return response(updated)
    }
    throw new Error('Unexpected mutation: ' + path)
  }, () => currentUsers)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Pending with memberships')))
  const userGroups = namedGroup('平台用户')
  const system = namedGroup('平台管理员', userGroups)
  expect(system.querySelectorAll('.cloud-platform-user')).toHaveLength(3)
  expect(groupCount(system)).toBe(3)
  for (const admin of admins) {
    const row = userRow(system, admin.displayName)
    expect(roleLabels(row)).toEqual([])
    expect(row.querySelector('button')?.disabled).toBe(true)
    expect(namedGroup('Organization One', userGroups).textContent).not.toContain(admin.displayName)
    expect(namedGroup('Organization Two', userGroups).textContent).not.toContain(admin.displayName)
  }
  const pending = namedGroup('待审批', userGroups)
  expect(pending.querySelectorAll('.cloud-platform-user')).toHaveLength(1)
  expect(groupCount(pending)).toBe(1)
  expect(pending.textContent).toContain('Pending user')
  expect(pending.textContent).not.toContain('Pending with memberships')
  const unassigned = namedGroup('未分配组织', userGroups)
  expect(groupCount(unassigned)).toBe(2)
  expect(unassigned.textContent).toContain('Active without memberships')
  expect(unassigned.textContent).toContain('Disabled without memberships')
  expect(unassigned.textContent).not.toContain('Disabled with membership')
  const first = namedGroup('Organization One', userGroups)
  const second = namedGroup('Organization Two', userGroups)
  expect(groupCount(first)).toBe(2)
  expect(groupCount(second)).toBe(3)
  expect(userRow(first, 'admin')).toBeDefined()
  expect(userRow(second, 'Disabled with membership').textContent).toContain('已停用')
  const pendingRow = userRow(second, 'Pending with memberships')
  expect(pendingRow.textContent).toContain('待审批')
  await act(async () => [...pendingRow.querySelectorAll('button')].find(button => button.textContent === '审核申请')!.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.querySelectorAll('select[aria-label^="组织角色："]')).toHaveLength(2)
  await act(async () => setValue(dialog.querySelector<HTMLSelectElement>('select')!, 'active'))
  await act(async () => dialog.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click())
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  const request = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(request?.[1]?.body as string)).toEqual({ status: 'active', memberships: users[2].memberships })
})

it('uses organization IDs for duplicate names and appends unknown membership and cluster groups without loss', async () => {
  const directory = [
    { id: 'same-a', name: 'Same organization', role: 'owner' },
    { id: 'same-b', name: 'Same organization', role: 'member' },
    { id: 'empty', name: 'Empty organization', role: 'viewer' },
  ]
  const account = { ...users[2], displayName: 'Multi-scope user', memberships: [
    { organizationId: 'same-a', role: 'owner' }, { organizationId: 'same-b', role: 'member' }, { organizationId: 'unknown-membership', role: 'viewer' },
  ] }
  const records = [
    { ...cluster, organizationId: 'same-a' },
    { ...cluster, id: 'unknown-cluster', name: 'Unknown organization cluster', organizationId: 'unknown-cluster-org' },
  ]
  const fetch = stubDirectory(path => { throw new Error('Unexpected mutation: ' + path) }, () => [account, users[1]], () => directory, () => records)
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Multi-scope user')))
  const userGroups = namedGroup('平台用户')
  const first = groupForId('same-a', userGroups)
  const second = groupForId('same-b', userGroups)
  expect(groupName(first)).not.toBe(groupName(second))
  expect(first.textContent).toContain('Same organization')
  expect(second.textContent).toContain('Same organization')
  expect(roleLabels(userRow(first, 'Multi-scope user'))).toEqual(['组织 Owner'])
  expect(roleLabels(userRow(second, 'Multi-scope user'))).toEqual(['成员'])
  const missingMembership = groupForId('unknown-membership', userGroups)
  expect(roleLabels(userRow(missingMembership, 'Multi-scope user'))).toEqual(['只读成员'])
  expect(groupCount(missingMembership)).toBe(1)
  expect(groupCount(groupForId('unknown-cluster-org', userGroups))).toBe(0)
  const clusterGroups = namedGroup('集群归属')
  expect(groupForId('same-a', clusterGroups).querySelectorAll('.cloud-platform-cluster')).toHaveLength(1)
  expect(groupForId('same-b', clusterGroups).querySelectorAll('.cloud-platform-cluster')).toHaveLength(0)
  expect(groupCount(groupForId('unknown-membership', clusterGroups))).toBe(0)
  const missingCluster = groupForId('unknown-cluster-org', clusterGroups)
  expect(groupCount(missingCluster)).toBe(1)
  expect(missingCluster.textContent).toContain('Unknown organization cluster')
  expect(groupCount(namedGroup('Empty organization', userGroups))).toBe(0)
  expect(groupCount(namedGroup('Empty organization', clusterGroups))).toBe(0)
  const visibleNames = [...userGroups.querySelectorAll<HTMLElement>('[role="group"]')].map(groupName)
  expect(visibleNames.indexOf(groupName(first))).toBeLessThan(visibleNames.indexOf(groupName(second)))
  expect(visibleNames.indexOf(groupName(second))).toBeLessThan(visibleNames.indexOf('Empty organization'))
  expect(visibleNames.indexOf('Empty organization')).toBeLessThan(visibleNames.indexOf(groupName(missingMembership)))
  await act(async () => [...userRow(first, 'Multi-scope user').querySelectorAll('button')].find(button => button.textContent === '编辑成员资格')!.click())
  let dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.querySelectorAll('select[aria-label^="组织角色："]')).toHaveLength(3)
  expect(dialog.querySelector<HTMLSelectElement>('[aria-label="组织角色：Same organization · same-a"]')?.value).toBe('owner')
  expect(dialog.querySelector<HTMLSelectElement>('[aria-label="组织角色：Same organization · same-b"]')?.value).toBe('member')
  expect(dialog.querySelector<HTMLSelectElement>('[aria-label="组织角色：unknown-membership"]')?.value).toBe('viewer')
  const membershipNames = [...dialog.querySelectorAll('.cloud-platform-membership-edit > strong')].map(node => node.textContent)
  expect(membershipNames).toEqual(['Same organization · same-a', 'Same organization · same-b', 'unknown-membership'])
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '取消')!.click())
  const pendingRow = userRow(namedGroup('待审批', userGroups), 'Pending user')
  await act(async () => [...pendingRow.querySelectorAll('button')].find(button => button.textContent === '审核申请')!.click())
  dialog = document.querySelector('[role="dialog"]')!
  const addOrganization = dialog.querySelector<HTMLSelectElement>('[aria-label="要添加的组织"]')!
  expect([...addOrganization.options].find(option => option.value === 'same-a')?.textContent).toBe('Same organization · same-a')
  expect([...addOrganization.options].find(option => option.value === 'same-b')?.textContent).toBe('Same organization · same-b')
  await act(async () => setValue(addOrganization, 'same-b'))
  expect(addOrganization.value).toBe('same-b')
  await act(async () => [...dialog.querySelectorAll('button')].find(button => button.textContent === '取消')!.click())
  const clusterRow = groupForId('same-a', clusterGroups).querySelector('.cloud-platform-cluster')!
  await act(async () => [...clusterRow.querySelectorAll('button')].find(button => button.textContent === '转移')!.click())
  dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Same organization · same-a')
  const transferTarget = dialog.querySelector<HTMLSelectElement>('[aria-label="转移目标组织"]')!
  expect([...transferTarget.options].find(option => option.value === 'same-b')?.textContent).toBe('Same organization · same-b')
  expect([...transferTarget.options].some(option => option.value === 'same-a')).toBe(false)
  await act(async () => setValue(transferTarget, 'same-b'))
  expect(transferTarget.value).toBe('same-b')
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST' || init?.method === 'PUT')).toBe(false)
})

it('keeps referenced users and clusters visible when the organization directory is empty', async () => {
  const account = { ...users[2], memberships: [{ organizationId: 'unlisted', role: 'viewer' }] }
  stubDirectory(path => { throw new Error('Unexpected mutation: ' + path) }, () => [account], () => [], () => [{ ...cluster, organizationId: 'unlisted' }])
  await renderPlatform()
  await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Active user')))
  const usersGroup = groupForId('unlisted', namedGroup('平台用户'))
  const clustersGroup = groupForId('unlisted', namedGroup('集群归属'))
  expect(groupCount(usersGroup)).toBe(1)
  expect(userRow(usersGroup, 'Active user')).toBeDefined()
  expect(groupCount(clustersGroup)).toBe(1)
  expect(clustersGroup.textContent).toContain('East cluster')
})
