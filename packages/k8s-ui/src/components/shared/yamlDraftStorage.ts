export const YAML_DRAFT_NAMESPACE = 'radar_yaml_draft:'
export const HUB_YAML_DRAFT_AUTHORITY_MARKER = `${YAML_DRAFT_NAMESPACE}hub_authority`

export interface YamlDraftResourceIdentity {
  group?: string
  kind: string
  namespace: string
  name: string
}

interface HubYamlDraftMarker {
  authority: string
  clusterScope?: string
}

export function yamlDraftStorageKey(scope: string | undefined, resource: YamlDraftResourceIdentity): string {
  if (scope === undefined) {
    return `${YAML_DRAFT_NAMESPACE}${resource.kind}/${resource.namespace}/${resource.name}`
  }
  const identity = [resource.group ?? '', resource.kind, resource.namespace, resource.name]
    .map(encodeURIComponent)
    .join('/')
  return `${YAML_DRAFT_NAMESPACE}scoped:${encodeURIComponent(scope)}:${identity}`
}

export function clearYamlDraftNamespace(): boolean {
  try {
    for (let index = sessionStorage.length - 1; index >= 0; index--) {
      const key = sessionStorage.key(index)
      if (key?.startsWith(YAML_DRAFT_NAMESPACE)) sessionStorage.removeItem(key)
    }
    return true
  } catch {
    return false
  }
}

export function reconcileHubYamlDraftScope(authority: string, clusterScope?: string): boolean {
  if (!authority) return false
  try {
    const rawMarker = sessionStorage.getItem(HUB_YAML_DRAFT_AUTHORITY_MARKER)
    let previous: HubYamlDraftMarker | null = null
    if (rawMarker) {
      try {
        const value: unknown = JSON.parse(rawMarker)
        if (
          value && typeof value === 'object' &&
          typeof (value as HubYamlDraftMarker).authority === 'string' &&
          ((value as HubYamlDraftMarker).clusterScope === undefined || typeof (value as HubYamlDraftMarker).clusterScope === 'string')
        ) {
          previous = value as HubYamlDraftMarker
        }
      } catch {
        previous = null
      }
    }

    const changed = !previous || previous.authority !== authority ||
      clusterScope !== undefined && previous.clusterScope !== clusterScope
    if (!changed) return true
    if (!clearYamlDraftNamespace()) return false
    sessionStorage.setItem(HUB_YAML_DRAFT_AUTHORITY_MARKER, JSON.stringify({
      authority,
      clusterScope: clusterScope ?? previous?.clusterScope,
    }))
    return true
  } catch {
    return false
  }
}
