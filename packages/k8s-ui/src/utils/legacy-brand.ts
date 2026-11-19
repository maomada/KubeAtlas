// Compatibility vocabulary for persisted data and peers predating the rebrand.
// New application identifiers and writes use KubeAtlas names.
export const LEGACY_YAML_DRAFT_NAMESPACE = 'radar_yaml_draft:'
export const LEGACY_YAML_DRAFT_CHANNEL = 'radar-hub-yaml-draft-purge'
export const EVIDENCE_MARKER_SOURCE = '(?:kubeatlas|radar):evidence'
export const EVIDENCE_ANCHOR_SOURCE = '(?:kubeatlas|radar)-evidence-'
export function canonicalEvidenceMarker(marker: string): string {
  return marker.replace('[[radar:evidence', '[[kubeatlas:evidence')
}
const legacyPreferencePrefixes = [
  'radar-theme', 'radar-portal-theme', 'radar-logs-', 'radar-columns-',
  'radar-sort-', 'radar-resource-checkboxes', 'radar-pinned-kinds',
  'radar-filters:', 'radar-last-kind:', 'radar-section:', 'radar-view:',
  'radar-update-check', 'radar.prometheus.autoConnect:', 'radar:debug:',
  'radar_return_path', 'radar_oidc_login_redirect', 'radar_proxy_reload',
  'radar-search-', 'radar-recent-', 'radar-incluster-',
  'radar-ai-', 'radar-manual-update-check', 'radar-update-dismissed',
  'radar-whats-new-seen', 'radar.cloudFunnel.seen', 'radar.cnpg.operations.v1',
  'radar.inClusterConsent.v2.', 'radar.navRail.pinned', 'radar.timeline.pinnedLanes',
]

function legacyPreferenceKey(key: string): string | undefined {
  if (!key.startsWith('kubeatlas')) return undefined
  const previous = 'radar' + key.slice('kubeatlas'.length)
  return legacyPreferencePrefixes.some(prefix => previous.startsWith(prefix)) ? previous : undefined
}

export function readProductStorage(storage: Storage, key: string): string | null {
  const current = storage.getItem(key)
  if (current !== null) return current
  const previousKey = legacyPreferenceKey(key)
  if (!previousKey) return null
  const previous = storage.getItem(previousKey)
  if (previous === null) return null
  try {
    storage.setItem(key, previous)
    storage.removeItem(previousKey)
  } catch { /* Preferences remain readable if storage is read-only. */ }
  return previous
}

export function removeProductStorage(storage: Storage, key: string): void {
  storage.removeItem(key)
  const previousKey = legacyPreferenceKey(key)
  if (previousKey) storage.removeItem(previousKey)
}

export const LEGACY_READ_CODES: Readonly<Record<string, string>> = {
  radar_credentials_rejected: 'kubeatlas_credentials_rejected',
  radar_forbidden: 'kubeatlas_forbidden',
  radar_kind_not_collected: 'kubeatlas_kind_not_collected',
  radar_namespace_not_collected: 'kubeatlas_namespace_not_collected',
}

const legacyHeaders: Readonly<Record<string, string>> = {
  'X-KubeAtlas-Resource-Read': 'X-Radar-Resource-Read',
  'X-KubeAtlas-Timeline-Epoch': 'X-Radar-Timeline-Epoch',
  'X-KubeAtlas-Timeline-Max-Seq': 'X-Radar-Timeline-Max-Seq',
  'X-KubeAtlas-Timeline-Min-Seq': 'X-Radar-Timeline-Min-Seq',
}

export function readProductHeader(headers: Headers, name: string): string | null {
  const current = headers.get(name)
  return current ?? (legacyHeaders[name] ? headers.get(legacyHeaders[name]) : null)
}

export function isKubeAtlasSource(source: unknown): boolean {
  return source === 'kubeatlas' || source === 'radar'
}

export function readUpgradeRequirement(error: Record<string, unknown>): unknown {
  return 'kubeatlasUpgrade' in error ? error.kubeatlasUpgrade : error.radarUpgrade
}

export function hasKubeAtlasEvidence(item: object): boolean {
  const value = item as Record<string, unknown>
  if ('kubeatlasEvidence' in value && 'radarEvidence' in value && value.kubeatlasEvidence !== value.radarEvidence) return false
  return ('kubeatlasEvidence' in value ? value.kubeatlasEvidence : value.radarEvidence) === true
}

export function readKubeAtlasVersion(value: object): string {
  const record = value as Record<string, unknown>
  const version = 'kubeatlasVersion' in record ? record.kubeatlasVersion : record.radarVersion
  return typeof version === 'string' ? version : ''
}

export function suspendedByProduct(annotations: Record<string, unknown> | undefined): boolean {
  return ['kubeatlas.io/suspended-prune', 'kubeatlas.io/suspended-selfheal',
    'radarhq.io/suspended-prune', 'radarhq.io/suspended-selfheal',
    'skyhook.io/suspended-prune', 'skyhook.io/suspended-selfheal']
    .some(key => Boolean(annotations?.[key]))
}

export function readRestoreValidation(annotations: Record<string, unknown> | undefined): unknown {
  const current = annotations?.['kubeatlas.io/restore-validation']
  const previous = annotations?.['radar.skyhook.io/restore-validation']
  if (current !== undefined && previous !== undefined && current !== previous) return undefined
  return current ?? previous
}
