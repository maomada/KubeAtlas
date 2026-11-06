// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { FleetWorkspace } from './FleetWorkspace'
import { Portal } from './Portal'
import type { AgentContext } from './fleet'
import { resourceItems } from './fleet'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
beforeEach(() => {
  window.history.replaceState({}, '', '/fleet')
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

it('renders Fleet inside the existing Portal shell with shared navigation and logout', () => {
  const html = renderToStaticMarkup(<Portal />)
  expect(html).toContain('aria-label="门户导航"')
  expect(html).toContain('退出登录')
  expect(html).toContain('多云分组与 Agent 上下文')
  expect(html).toContain('href="/"')
  expect(html).not.toContain('<iframe')
  window.history.replaceState({}, '', '/')
  const directory = renderToStaticMarkup(<Portal />)
  expect(directory).toContain('搜索集群')
  expect(directory).toContain('href="/fleet"')
  expect(directory).not.toContain('aria-label="Agent 上下文预览"')
})

it('discards an older context response after switching scope', async () => {
  let resolveContext!: (response: Response) => void
  const pending = new Promise<Response>(resolve => { resolveContext = resolve })
  const fetch = vi.fn((url: string) => {
    if (url.endsWith('/context')) return pending
    if (url.endsWith('/fleets')) return Promise.resolve(new Response(JSON.stringify([{ id: 'prod', name: 'Production', clusterIds: ['east'] }])))
    return Promise.resolve(new Response(JSON.stringify([{ clusterId: 'east', name: 'East', state: 'stale', observedAt: '2026-01-01T00:00:00Z', documents: [], limitations: [] }])))
  })
  vi.stubGlobal('fetch', fetch)
  await act(async () => root.render(<FleetWorkspace registeredClusters={[]} />))
  const button = [...host.querySelectorAll('button')].find(button => button.textContent === '预览上下文')!
  await act(async () => button.click())
  expect(host.textContent).toContain('正在读取同步数据')
  const select = host.querySelector('select')!
  await act(async () => {
    select.value = 'cluster:east'
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  const old: AgentContext = { mode: 'multi_cloud', clusters: [], limitations: ['OLD_RESPONSE_SHOULD_NOT_APPEAR'] }
  await act(async () => resolveContext(new Response(JSON.stringify(old))))
  expect(host.textContent).not.toContain('OLD_RESPONSE_SHOULD_NOT_APPEAR')
  expect(host.textContent).not.toContain('正在读取同步数据')
  expect(host.querySelector('pre')).toBeNull()
  expect(fetch.mock.calls.every(([url]) => url.startsWith('/api/fleet/'))).toBe(true)
})

it('only extracts selectable canonical resource list items', () => {
  expect(resourceItems({ tool: 'list_resources', input: { kind: 'pods' }, data: { items: [{ name: 'api', namespace: 'default' }, null, { namespace: 'default' }] } })).toEqual([{ name: 'api', namespace: 'default' }])
  expect(resourceItems({ tool: 'get_resource', input: {}, data: { items: [{ name: 'api' }] } })).toEqual([])
})

it('keeps a pending tool catalog when opening a document in the same scope', async () => {
  let resolveTools!: (response: Response) => void
  const pendingTools = new Promise<Response>(resolve => { resolveTools = resolve })
  const cluster = { clusterId: 'east', state: 'current', observedAt: '2026-01-01T00:00:00Z', receivedAt: '', documents: [{ tool: 'get_dashboard', input: {}, data: { nodes: 1 } }], limitations: [] }
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (url.endsWith('/tools')) return pendingTools
    const data = url.endsWith('/context') ? { mode: 'multi_cloud', clusters: [cluster], limitations: [] }
      : url.endsWith('/fleets') ? [{ id: 'prod', name: 'Production', clusterIds: ['east'] }] : [cluster]
    return Promise.resolve(new Response(JSON.stringify(data)))
  }))
  await act(async () => root.render(<FleetWorkspace registeredClusters={[]} />))
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === '预览上下文')!.click())
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'get_dashboard')!.click())
  await act(async () => resolveTools(new Response(JSON.stringify([{ clusterId: 'east', state: 'connected', tools: [{ name: 'inspect', inputSchema: { type: 'object' } }] }]))))
  expect(host.textContent).toContain('inspect')
  expect(host.textContent).not.toContain('正在读取工具通道')
})
