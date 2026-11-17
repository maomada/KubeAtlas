// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { ApiError, fetchResourceAvailability } from './client'
import { setApiBase } from './config'
import { splitStory, storyReferenceIndex } from '../components/diagnose/investigationStory'

afterEach(() => { vi.unstubAllGlobals(); setApiBase('/api') })

it.each([[200, 'present'], [404, 'missing']] as const)('uses old-Agent HEAD evidence at status %s', async (status, expected) => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status, headers: { 'X-Radar-Resource-Read': 'true' } })))
  expect(await fetchResourceAvailability('configmaps', 'qa', 'config')).toBe(expected)
})

it('does not claim confirmed absence when current metadata contradicts the old lookup header', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 404, headers: {
    'X-KubeAtlas-Resource-Read': 'false', 'X-Radar-Resource-Read': 'true',
  } })))
  await expect(fetchResourceAvailability('configmaps', 'qa', 'config')).rejects.toMatchObject({ status: 404 })
})

it('normalizes only the own error code without rewriting embedded resource data', () => {
  const resource = { metadata: { name: 'radar-owned-by-user' }, spec: { radarVersion: 'literal user field' } }
  const error = new ApiError('denied', 503, { error_code: 'radar_forbidden', resource })
  expect(error.data?.error_code).toBe('kubeatlas_forbidden')
  expect(error.data?.resource).toBe(resource)
  expect(resource.spec.radarVersion).toBe('literal user field')
})

it('reads legacy story citations while preserving code and untrusted marker boundaries', () => {
  const result = splitStory('[[radar:evidence=0]]\n\nQuoted `[[radar:evidence=1]]` and [[radar:evidence=2]].')
  expect(result.segments).toContainEqual({ kind: 'placement', index: 0 })
  expect(JSON.stringify(result.segments)).toContain('#kubeatlas-evidence-2')
  expect(JSON.stringify(result.segments)).toContain('`[[radar:evidence=1]]`')
  expect(storyReferenceIndex('#radar-evidence-2')).toBe(2)
  expect(storyReferenceIndex('#radar-evidence-a.*')).toBeUndefined()
})
