// @vitest-environment jsdom
import { beforeEach, expect, it } from 'vitest'
import {
  clearYamlDraftNamespace,
  HUB_YAML_DRAFT_AUTHORITY_MARKER,
  reconcileHubYamlDraftScope,
  yamlDraftStorageKey,
} from './yamlDraftStorage'

beforeEach(() => sessionStorage.clear())

const resource = { group: 'apps', kind: 'Deployment', namespace: 'qa', name: 'agent' }

it('uses legacy keys by default and scopes by group, kind, namespace, and name when requested', () => {
  expect(yamlDraftStorageKey(undefined, resource)).toBe('radar_yaml_draft:Deployment/qa/agent')
  expect(yamlDraftStorageKey('owner-a:cluster-a', resource)).toBe(
    'radar_yaml_draft:scoped:owner-a%3Acluster-a:apps/Deployment/qa/agent',
  )
  expect(yamlDraftStorageKey('scope', { ...resource, group: 'example.test' })).not.toBe(
    yamlDraftStorageKey('scope', resource),
  )
})

it('purges legacy and scoped drafts on Hub entry, preserving unrelated session state', () => {
  sessionStorage.setItem('radar_yaml_draft:ConfigMap/qa/config', 'legacy')
  sessionStorage.setItem(yamlDraftStorageKey('owner-a:cluster-a', resource), 'owner-unsaved')
  sessionStorage.setItem('activeOrganizationId', 'org-a')

  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  expect(sessionStorage.getItem('radar_yaml_draft:ConfigMap/qa/config')).toBeNull()
  expect(sessionStorage.getItem(yamlDraftStorageKey('owner-a:cluster-a', resource))).toBeNull()
  expect(sessionStorage.getItem('activeOrganizationId')).toBe('org-a')
  expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toContain('authority-owner-a')
})

it('preserves the same authority and cluster across reload and Home navigation', () => {
  const key = yamlDraftStorageKey('scope-owner-a-cluster-a', resource)
  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  sessionStorage.setItem(key, 'owner-unsaved')
  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  expect(reconcileHubYamlDraftScope('authority-owner-a')).toBe(true)
  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  expect(sessionStorage.getItem(key)).toBe('owner-unsaved')
})

it.each([
  ['organization switch', 'authority-org-a', 'authority-org-b', 'cluster-a:/c/cluster-a/api'],
  ['same-org role demotion', 'authority-owner-a', 'authority-viewer-a', 'cluster-a:/c/cluster-a/api'],
  ['owner account switch', 'authority-owner-a', 'authority-owner-b', 'cluster-a:/c/cluster-a/api'],
  ['disable', 'authority-active', 'authority-disabled', 'cluster-a:/c/cluster-a/api'],
  ['account switch', 'authority-account-a', 'authority-account-b', 'cluster-a:/c/cluster-a/api'],
  ['same-org cluster switch', 'authority-owner-a', 'authority-owner-a', 'cluster-b:/c/cluster-b/api'],
])('invalidates on %s and prevents switch-back resurrection', (_label, initialAuthority, nextAuthority, nextCluster) => {
  const oldKey = yamlDraftStorageKey('old-scope', resource)
  expect(reconcileHubYamlDraftScope(initialAuthority, 'cluster-a:/c/cluster-a/api')).toBe(true)
  sessionStorage.setItem(oldKey, 'owner-unsaved')
  expect(reconcileHubYamlDraftScope(nextAuthority, nextCluster)).toBe(true)
  expect(sessionStorage.getItem(oldKey)).toBeNull()
  sessionStorage.setItem(oldKey, 'new-authority-draft')
  expect(reconcileHubYamlDraftScope(initialAuthority, 'cluster-a:/c/cluster-a/api')).toBe(true)
  expect(sessionStorage.getItem(oldKey)).toBeNull()
})

it('does not restore drafts after logout and same-authority relogin', () => {
  const key = yamlDraftStorageKey('owner-a-cluster-a', resource)
  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  sessionStorage.setItem(key, 'owner-unsaved')
  expect(clearYamlDraftNamespace()).toBe(true)
  expect(reconcileHubYamlDraftScope('authority-owner-a', 'cluster-a:/c/cluster-a/api')).toBe(true)
  expect(sessionStorage.getItem(key)).toBeNull()
})

it('removes only the YAML draft namespace', () => {
  sessionStorage.setItem('radar_yaml_draft:legacy', 'draft')
  sessionStorage.setItem(HUB_YAML_DRAFT_AUTHORITY_MARKER, 'marker')
  sessionStorage.setItem('unrelated', 'keep')
  expect(clearYamlDraftNamespace()).toBe(true)
  expect(sessionStorage.getItem('radar_yaml_draft:legacy')).toBeNull()
  expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toBeNull()
  expect(sessionStorage.getItem('unrelated')).toBe('keep')
})
