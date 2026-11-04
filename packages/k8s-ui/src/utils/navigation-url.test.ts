import { describe, expect, it } from 'vitest'
import { resolveNavigationUrl } from './navigation-url'

const base = 'https://atlas.example/clusters/current'

describe('cluster navigation URLs', () => {
  it.each([
    ['https://cluster.example/path', 'https://cluster.example/path'],
    ['http://localhost:9280/', 'http://localhost:9280/'],
    ['/clusters/next', 'https://atlas.example/clusters/next'],
    ['../next', 'https://atlas.example/next'],
    ['//cluster.example/path', 'https://cluster.example/path'],
  ])('allows HTTP(S) and relative targets: %s', (input, expected) => {
    expect(resolveNavigationUrl(input, base)).toBe(expected)
  })

  it.each([
    'javascript:alert(1)', ' JAVAscript:alert(1)', 'java\nscript:alert(1)',
    'data:text/html,<script>alert(1)</script>', 'file:///etc/passwd',
    'blob:https://atlas.example/id', 'https://user:password@cluster.example/', 'https://[',
  ])('rejects executable, credential-bearing and malformed targets: %s', input => {
    expect(resolveNavigationUrl(input, base)).toBeNull()
  })
})
