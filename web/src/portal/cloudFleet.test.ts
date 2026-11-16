import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchFleet } from './cloudFleet'
import type { HubCluster } from './cloudApi'

const cluster = (id: string, state: HubCluster['state'] = 'connected'): HubCluster => ({ id, organizationId: 'org-1', name: id, description: '', environment: '', region: '', createdAt: '', updatedAt: '', configured: true, state })
afterEach(() => vi.unstubAllGlobals())

describe('live fleet requests', () => {
  it('retains source identity and failures without fabricating empty success data', async () => {
    const fetch = vi.fn((url: string) => Promise.resolve(url.includes('/denied/')
      ? new Response('{"error_code":"permission_denied","message":"RBAC denied"}', { status: 403, headers: { 'Content-Type': 'application/json' } })
      : new Response('{"items":["pod-a"]}', { headers: { 'Content-Type': 'application/json' } })))
    vi.stubGlobal('fetch', fetch)
    const results = await fetchFleet([cluster('east'), cluster('denied'), cluster('offline', 'offline')], '/resources/pods', new AbortController().signal)
    expect(results.map(result => result.cluster.id)).toEqual(['east', 'denied', 'offline'])
    expect(results[0].data).toEqual({ items: ['pod-a'] })
    expect(results[1]).toMatchObject({ error: { status: 403, code: 'permission_denied' } })
    expect(results[2]).toMatchObject({ error: { code: 'cluster_unavailable' } })
    expect(results[1].data).toBeUndefined()
    expect(results[2].data).toBeUndefined()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('bounds concurrent calls and cancels queued work when the view is left', async () => {
    const controller = new AbortController()
    const fetch = vi.fn((_url: string, options: RequestInit) => new Promise<Response>((_resolve, reject) => {
      options.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    vi.stubGlobal('fetch', fetch)
    const request = fetchFleet(Array.from({ length: 12 }, (_, index) => cluster(`cluster-${index}`)), '/dashboard', controller.signal)
    expect(fetch).toHaveBeenCalledTimes(4)
    controller.abort()
    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetch).toHaveBeenCalledTimes(4)
  })
})
