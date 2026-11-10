import { expect, it } from 'vitest'
import { cloudViewHref } from './cloudNavigation'

it('preserves native cross-view scope without carrying resource drawer filters', () => {
  const href = cloudViewHref('/topology', true, 'demo-secondary', '?namespaces=demo-shop&ai-run=run-1&resource=demo-shop/pod&full=1')
  expect(href).toBe('/c/demo-secondary/topology?namespaces=demo-shop&ai-run=run-1')
  expect(cloudViewHref('/helm', true, 'demo-secondary', '')).toBe('/c/demo-secondary/helm')
})

it('preserves the selected cluster between fleet and native destinations', () => {
  expect(cloudViewHref('/issues', false, 'demo-secondary', '?namespaces=demo-shop')).toBe('/issues?cluster=demo-secondary')
  expect(cloudViewHref('/resources', true, 'demo-secondary', '?cluster=demo-secondary')).toBe('/c/demo-secondary/resources')
  expect(cloudViewHref('/resources', true, undefined, '')).toBe('/resources')
})
