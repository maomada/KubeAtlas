// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import type { QueryClient } from '@tanstack/react-query'
import { expect, it, vi } from 'vitest'
import { PortalEntry } from './CloudPortal'

const scope = {
  active: 'demo-shop', actives: ['demo-shop'], accessibleNamespaces: ['demo-shop', 'demo-staging'],
  mode: 'namespace', cacheScoped: false, authoritative: true, canClearNamespace: true,
}

vi.mock('../RadarApp', () => ({
  RadarApp: ({ queryClient }: { queryClient: QueryClient }) => <button onClick={() => {
    queryClient.setQueryData(['namespace-scope'], { ...scope, active: 'demo-staging', actives: ['demo-staging'] })
  }}>Change native namespace</button>,
}))

vi.mock('@maomada/k8s-ui', async importOriginal => {
  const actual = await importOriginal<typeof import('@maomada/k8s-ui')>()
  return {
    ...actual,
    NamespacePicker: ({ onApply, scope }: { onApply: (namespaces: string[]) => void; scope?: { active?: string } }) =>
      <>
        <span>{scope?.active}</span>
        <button onClick={() => onApply(['demo-staging'])}>Apply host namespace</button>
      </>,
  }
})

it('shares the native namespace cache with the header and preserves sidebar scope', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/pods?namespaces=demo-shop')
  const fetch = vi.fn<typeof globalThis.fetch>(async input => {
    const responses: Record<string, unknown> = {
      '/api/hub': { enabled: true, userId: 'test-user' },
      '/api/hub/clusters': { clusters: [{ id: 'alpha', name: 'Alpha', state: 'connected' }] },
      '/api/mcp/credentials': { credentials: [], serverTime: '2026-01-01T00:00:00Z' },
      '/c/alpha/api/cluster/namespace-scope': scope,
      '/c/alpha/api/api-resources': [],
    }
    if (!(String(input) in responses)) throw new Error(`Unexpected request: ${input}`)
    return new Response(JSON.stringify(responses[String(input)]), { headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.textContent).toContain('Change native namespace')))
    const topology = [...host.querySelectorAll('a')].find(link => link.textContent === 'Topology')
    expect(topology?.getAttribute('href')).toBe('/c/alpha/topology?namespaces=demo-shop')
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop')))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Change native namespace')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging')))
    expect(host.querySelector('header')?.textContent).not.toContain('demo-shop')
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})

it('reconciles the namespace cache after an uncertain host write', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  window.history.replaceState({}, '', '/c/alpha/resources/pods?namespaces=demo-shop')
  const updatedScope = { ...scope, active: 'demo-staging', actives: ['demo-staging'] }
  let scopeReads = 0
  let apiResourceReads = 0
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = String(input)
    if (path === '/api/hub') return Response.json({ enabled: true, userId: 'test-user' })
    if (path === '/api/hub/clusters') return Response.json({ clusters: [{ id: 'alpha', name: 'Alpha', state: 'connected' }] })
    if (path === '/api/mcp/credentials') return Response.json({ credentials: [], serverTime: '2026-01-01T00:00:00Z' })
    if (path === '/c/alpha/api/cluster/namespace-scope') {
      scopeReads++
      return Response.json(scopeReads === 1 ? scope : updatedScope)
    }
    if (path === '/c/alpha/api/api-resources') {
      apiResourceReads++
      return Response.json([])
    }
    if (path === '/c/alpha/api/cluster/namespace') {
      expect(init?.method).toBe('POST')
      expect(init?.body).toBe(JSON.stringify({ namespaces: ['demo-staging'] }))
      return Response.json(
        { error_code: 'source_unavailable', message: 'Source response lost', outcome: 'unknown' },
        { status: 502 },
      )
    }
    throw new Error(`Unexpected request: ${input}`)
  })
  vi.stubGlobal('fetch', fetch)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<PortalEntry />))
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-shop')))
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Apply host namespace')!.click())
    await act(async () => vi.waitFor(() => expect(host.querySelector('header')?.textContent).toContain('demo-staging')))
    expect(scopeReads).toBe(2)
    expect(apiResourceReads).toBe(1)
  } finally {
    act(() => root.unmount())
    host.remove()
    window.history.replaceState({}, '', '/')
    vi.unstubAllGlobals()
  }
})
