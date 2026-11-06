import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('KubeAtlas branding', () => {
  it('keeps the Portal title and favicon explicitly branded', () => {
    const html = readFileSync(new URL('../portal.html', import.meta.url), 'utf8')
    expect(html).toContain('<title>集群门户 · KubeAtlas</title>')
    expect(html).toContain('href="/src/portal/favicon.svg"')
  })
})
