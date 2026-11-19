// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest'
import { readProductStorage, removeProductStorage, readProductHeader, hasKubeAtlasEvidence,
  isKubeAtlasSource, suspendedByProduct, LEGACY_YAML_DRAFT_CHANNEL } from './legacy-brand'
import { canonicalReadCode } from './read-actions'
import { getKubeAtlasUpgradeRequirement } from '../types/fetch-error'
import { clearYamlDraftNamespace, reconcileHubYamlDraftScope, HUB_YAML_DRAFT_AUTHORITY_MARKER } from '../components/shared/yamlDraftStorage'
import { getArgoApplicationStatus } from '../components/resources/resource-utils-argo'
import { getCNPGRestoreValidation } from '../components/cnpg/workspace'

beforeEach(() => { localStorage.clear(); sessionStorage.clear() })

it('migrates an old preference without overwriting a current preference or another app key', () => {
  localStorage.setItem('radar-theme', 'dark')
  localStorage.setItem('unrelated', 'keep')
  expect(readProductStorage(localStorage, 'kubeatlas-theme')).toBe('dark')
  expect(localStorage.getItem('kubeatlas-theme')).toBe('dark')
  expect(localStorage.getItem('radar-theme')).toBeNull()
  localStorage.setItem('radar-theme', 'light')
  expect(readProductStorage(localStorage, 'kubeatlas-theme')).toBe('dark')
  removeProductStorage(localStorage, 'kubeatlas-theme')
  expect(localStorage.getItem('radar-theme')).toBeNull()
  expect(localStorage.getItem('kubeatlas-theme')).toBeNull()
  expect(localStorage.getItem('unrelated')).toBe('keep')
})

it('does not migrate old drafts or unknown application keys as harmless preferences', () => {
  sessionStorage.setItem('radar_yaml_draft:ConfigMap/qa/config', 'private draft')
  localStorage.setItem('radar-unrelated-app', 'unrelated')
  expect(readProductStorage(sessionStorage, 'kubeatlas_yaml_draft:ConfigMap/qa/config')).toBeNull()
  expect(readProductStorage(localStorage, 'kubeatlas-unrelated-app')).toBeNull()
})

it('purges both draft generations even with an unchanged current authority marker', () => {
  expect(reconcileHubYamlDraftScope('owner-a', 'cluster-a')).toBe(true)
  sessionStorage.setItem('kubeatlas_yaml_draft:scoped:current', 'current draft')
  sessionStorage.setItem('radar_yaml_draft:scoped:old-authority', 'old draft')
  expect(reconcileHubYamlDraftScope('owner-a', 'cluster-a')).toBe(true)
  expect(sessionStorage.getItem('radar_yaml_draft:scoped:old-authority')).toBeNull()
  expect(sessionStorage.getItem('kubeatlas_yaml_draft:scoped:current')).toBe('current draft')
  expect(clearYamlDraftNamespace()).toBe(true)
  expect(sessionStorage.getItem('kubeatlas_yaml_draft:scoped:current')).toBeNull()
  expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toBeNull()
  expect(LEGACY_YAML_DRAFT_CHANNEL).toBe('radar-hub-yaml-draft-purge')
})

it('fails closed when an old authority draft cannot be removed', () => {
  sessionStorage.setItem('radar_yaml_draft:scoped:owner-a', 'private draft')
  const remove = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('storage denied') })
  try {
    expect(reconcileHubYamlDraftScope('owner-b', 'cluster-a')).toBe(false)
    expect(clearYamlDraftNamespace()).toBe(false)
    expect(sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)).toBeNull()
  } finally { remove.mockRestore() }
})

it.each([
  ['radar_forbidden', 'kubeatlas_forbidden'],
  ['radar_credentials_rejected', 'kubeatlas_credentials_rejected'],
  ['radar_kind_not_collected', 'kubeatlas_kind_not_collected'],
  ['radar_namespace_not_collected', 'kubeatlas_namespace_not_collected'],
])('classifies an old Agent read code %s without discarding its meaning', (old, current) => {
  expect(canonicalReadCode(old)).toBe(current)
  expect(canonicalReadCode(current)).toBe(current)
})

it('reads old response headers but does not invent aliases for unknown metadata', () => {
  const headers = new Headers({ 'X-Radar-Resource-Read': 'true', 'X-Radar-Timeline-Epoch': 'old-epoch', 'X-Radar-Trusted': 'true' })
  expect(readProductHeader(headers, 'X-KubeAtlas-Resource-Read')).toBe('true')
  expect(readProductHeader(headers, 'X-KubeAtlas-Timeline-Epoch')).toBe('old-epoch')
  expect(readProductHeader(headers, 'X-KubeAtlas-Trusted')).toBeNull()
  headers.set('X-KubeAtlas-Resource-Read', 'false')
  expect(readProductHeader(headers, 'X-KubeAtlas-Resource-Read')).toBe('false')
})

it('retains old validated evidence but rejects conflicting flags and truthy non-booleans', () => {
  expect(hasKubeAtlasEvidence({ radarEvidence: true })).toBe(true)
  expect(hasKubeAtlasEvidence({ kubeatlasEvidence: true })).toBe(true)
  expect(hasKubeAtlasEvidence({ kubeatlasEvidence: false, radarEvidence: true })).toBe(false)
  expect(hasKubeAtlasEvidence({ kubeatlasEvidence: true, radarEvidence: false })).toBe(false)
  expect(hasKubeAtlasEvidence({ radarEvidence: 'true' })).toBe(false)
  expect(isKubeAtlasSource('radar')).toBe(true)
  expect(isKubeAtlasSource('unknown')).toBe(false)
})

it('retains upgrade detection from older hosts without falling back past an explicit invalid current value', () => {
  expect(getKubeAtlasUpgradeRequirement({ radarUpgrade: { feature: 'Capacity', currentVersion: '1.0' } })?.feature).toBe('Capacity')
  expect(getKubeAtlasUpgradeRequirement({ kubeatlasUpgrade: false, radarUpgrade: { feature: 'Capacity' } })).toBeNull()
})

it.each(['kubeatlas.io', 'radarhq.io', 'skyhook.io'])('recognizes preserved Argo pause state in %s', namespace => {
  const annotations = { [namespace + '/suspended-prune']: 'false' }
  expect(suspendedByProduct(annotations)).toBe(true)
  expect(getArgoApplicationStatus({ metadata: { annotations }, spec: {}, status: { health: { status: 'Missing' } } }).text).toBe('Suspended')
})

it('retains recorded restore notes bound to the same Cluster UID and rejects contradictory generations', () => {
  const raw = JSON.stringify({ version: 1, checked: 'read back the restored data', recordedAt: '2026-11-17T00:00:00Z', target: { uid: 'same-cluster' } })
  const cluster = { metadata: { uid: 'same-cluster', annotations: { 'radar.skyhook.io/restore-validation': raw } } }
  expect(getCNPGRestoreValidation(cluster)?.checked).toBe('read back the restored data')
  expect(getCNPGRestoreValidation({ metadata: { ...cluster.metadata, uid: 'different-cluster' } })).toBeNull()
  expect(getCNPGRestoreValidation({ metadata: { ...cluster.metadata, annotations: { ...cluster.metadata.annotations, 'kubeatlas.io/restore-validation': 'different' } } })).toBeNull()
})
