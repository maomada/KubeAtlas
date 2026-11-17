import { afterEach, describe, expect, it, vi } from 'vitest'
import { setApiBase } from '../api/config'
import { cloudRequest, clusterBase, clusterPage, selectedCluster } from './cloudApi'
import { credentialsRequest } from './credentials'

afterEach(() => {
  setApiBase('/api')
  vi.unstubAllGlobals()
})

describe('Cloud portal routing', () => {
  it('keeps management requests on the Hub while KubeAtlasApp changes its API base', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(() => Promise.resolve(new Response('{"clusters":[]}', { headers: { 'Content-Type': 'application/json' } })))
    vi.stubGlobal('fetch', fetch)
    setApiBase('/c/alpha/api')
    await cloudRequest('/hub/clusters')
    expect(fetch.mock.calls[0][0]).toBe('/api/hub/clusters')
    await cloudRequest('/resources/pods', { base: clusterBase('beta') })
    expect(fetch.mock.calls[1][0]).toBe('/c/beta/api/resources/pods')
    await credentialsRequest()
    expect(fetch.mock.calls[2][0]).toBe('/api/mcp/credentials')
  })

  it('rejects malformed cluster identifiers rather than retargeting a request', () => {
    for (const id of ['../api', 'a/b', '', '%2f', 'x?y', 'x#y', 'x\\y', 'UPPER', 'a.b', 'a_b', 'a'.repeat(64)]) expect(() => clusterBase(id)).toThrow()
    expect(selectedCluster('/c/a%2fb/resources')).toBeUndefined()
    expect(selectedCluster('/c/%/resources')).toBeUndefined()
    expect(selectedCluster('/c/team-prod/resources/pods')).toBe('team-prod')
    expect(clusterPage('team-prod', '/topology')).toBe('/c/team-prod/topology')
  })

  it('preserves source permission failures, with no empty successful fallback', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error_code: 'permission_denied', message: 'Access denied', requestId: 'req-1',
    }), { status: 403, headers: { 'Content-Type': 'application/json' } })))
    await expect(cloudRequest('/resources/secrets', { base: clusterBase('team-prod') })).rejects.toMatchObject({
      status: 403, code: 'permission_denied', requestId: 'req-1',
    })
  })

  it('keeps the Hub session redirect for HTTP 401 responses', async () => {
    const replace = vi.fn()
    vi.stubGlobal('window', { location: { replace } })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error_code: 'session_expired', message: 'Session expired',
    }), { status: 401, headers: { 'Content-Type': 'application/json' } })))

    await expect(cloudRequest('/hub/enrollments/command')).rejects.toMatchObject({
      status: 401, code: 'session_expired', message: 'Session expired',
    })
    expect(replace).toHaveBeenCalledWith('/login?expired=1')
  })

  it('maps fetch TypeErrors for any cloud path and retains the transport cause', async () => {
    const transportFailure = new TypeError('Failed to fetch')
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(transportFailure))

    await expect(cloudRequest('/resources/pods', { base: clusterBase('team-prod') })).rejects.toMatchObject({
      message: '网络请求失败，请检查网络连接后重试。',
      cause: transportFailure,
    })
  })

  it('preserves aborted fetch failures without applying the transport fallback', async () => {
    const controller = new AbortController()
    const abortFailure = new DOMException('Aborted', 'AbortError')
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abortFailure))

    await expect(cloudRequest('/hub/clusters', { signal: controller.signal })).rejects.toBe(abortFailure)

    controller.abort()
    const abortedFailure = new TypeError('Fetch rejected after cancellation')
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abortedFailure))
    await expect(cloudRequest('/hub/clusters', { signal: controller.signal })).rejects.toBe(abortedFailure)
  })

  it('does not relabel a request-body serialization TypeError as a network failure', async () => {
    const body: Record<string, unknown> = {}
    body.self = body
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)

    await expect(cloudRequest('/hub/clusters', { method: 'POST', body })).rejects.toThrow(/circular/i)
    expect(fetch).not.toHaveBeenCalled()
  })

  it('preserves HTTP server messages and codes', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error_code: 'service_unavailable', message: 'Hub maintenance in progress',
    }), { status: 503, headers: { 'Content-Type': 'application/json' } })))

    await expect(cloudRequest('/hub/clusters')).rejects.toMatchObject({
      status: 503, code: 'service_unavailable', message: 'Hub maintenance in progress',
    })
  })
})
