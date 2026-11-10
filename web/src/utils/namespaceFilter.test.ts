import { expect, it } from 'vitest'
import { namespaceFilterDisabled } from './namespaceFilter'
import type { APIResource } from '../types'

it('explains cluster-wide views without disabling namespaced workspaces', () => {
  for (const [view, path] of [['cost', '/cost'], ['capacity', '/capacity/demand'], ['checks', '/checks/upgrade'], ['gitops', '/gitops/detail/argocd/demo/app']]) {
    expect(namespaceFilterDisabled(view, path)).toMatchObject({ disabled: true, tooltip: expect.any(String) })
  }
  for (const [view, path] of [['topology', '/topology'], ['cost', '/cost/rightsizing'], ['cnpg', '/cnpg'], ['gitops', '/gitops']]) {
    expect(namespaceFilterDisabled(view, path).disabled).toBe(false)
  }
})

it('uses discovered resource scope instead of assuming every kind is namespaced', () => {
  const resources = [
    { name: 'nodes', kind: 'Node', group: '', version: 'v1', namespaced: false },
    { name: 'pods', kind: 'Pod', group: '', version: 'v1', namespaced: true },
  ] as APIResource[]
  expect(namespaceFilterDisabled('resources', '/resources/nodes', '', resources).disabled).toBe(true)
  expect(namespaceFilterDisabled('resources', '/resources/pods', '', resources).disabled).toBe(false)
})
