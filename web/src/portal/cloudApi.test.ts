import { afterEach, describe, expect, it, vi } from 'vitest'
import { setApiBase } from '../api/config'
import { cloudRequest, clusterBase, clusterPage, selectedCluster } from './cloudApi'
import { credentialsRequest } from './credentials'

afterEach(() => {
  setApiBase('/api')
  vi.unstubAllGlobals()
})

describe('Cloud portal routing', () => {
  it('keeps management requests on the Hub while RadarApp changes its API base', async () => {
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
})
