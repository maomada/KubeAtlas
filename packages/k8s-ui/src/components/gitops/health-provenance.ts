// Copy and predicate for the "KubeAtlas" marker shown beside a problem the
// GitOps controller did not assess itself — Argo CD 3 keeps per-resource
// health out of the Application object, so KubeAtlas reads the live resource
// instead and must say so. Shared by the tree node card and the Changes
// table so the two never drift.

// One sentence in plain words; the engine's own message follows it when
// there is one.
export const KUBEATLAS_HEALTH_NOTE = "Found by KubeAtlas. Argo CD's own health for this resource isn't available."

export interface KubeAtlasHealthSubject {
  health?: string
  healthSource?: string
  healthMessage?: string
}

// kubeatlasHealthNote returns the marker tooltip for a row/node whose problem
// came from KubeAtlas rather than the controller, or '' when there is no
// problem to mark or the controller assessed it.
export function kubeatlasHealthNote(subject: KubeAtlasHealthSubject): string {
  if (subject.healthSource !== 'kubeatlas') return ''
  if (subject.health !== 'Degraded' && subject.health !== 'Missing') return ''
  return subject.healthMessage ? `${KUBEATLAS_HEALTH_NOTE} ${subject.healthMessage}` : KUBEATLAS_HEALTH_NOTE
}
