import { describe, expect, test } from 'vitest'

import { KUBEATLAS_HEALTH_NOTE, kubeatlasHealthNote } from './health-provenance'
import { APP_TREE_API_ERROR_NOTICE, APP_TREE_HEALTH_NOTICE, APP_TREE_NO_FINDINGS_NOTICE, APP_TREE_PERSIST_REMEDY, REMOTE_DESTINATION_NOTICE, REMOTE_FLUX_TARGET_NOTICE, hasKubeAtlasFinding, healthSourceNoticeKind } from './GitOpsHealthSourceNotice'

describe('kubeatlasHealthNote', () => {
  test('marks only KubeAtlas-sourced problems', () => {
    expect(kubeatlasHealthNote({ health: 'Degraded', healthSource: 'kubeatlas' })).toBe(KUBEATLAS_HEALTH_NOTE)
    expect(kubeatlasHealthNote({ health: 'Missing', healthSource: 'kubeatlas', healthMessage: 'not in cluster' })).toBe(`${KUBEATLAS_HEALTH_NOTE} not in cluster`)
  })
  test('is silent for the controller, for healthy values, and for unknown sources', () => {
    expect(kubeatlasHealthNote({ health: 'Degraded', healthSource: 'controller' })).toBe('')
    expect(kubeatlasHealthNote({ health: 'Degraded', healthSource: 'controllerApi' })).toBe('')
    expect(kubeatlasHealthNote({ health: 'Healthy', healthSource: 'kubeatlas' })).toBe('')
    expect(kubeatlasHealthNote({ health: 'Degraded' })).toBe('')
    expect(kubeatlasHealthNote({ health: 'Degraded', healthSource: 'something-newer' })).toBe('')
  })
  test('copy stays in plain words', () => {
    for (const copy of [KUBEATLAS_HEALTH_NOTE, APP_TREE_HEALTH_NOTICE, REMOTE_DESTINATION_NOTICE, REMOTE_FLUX_TARGET_NOTICE]) {
      expect(copy).not.toMatch(/appTree|resourceHealthSource|persist|Tier|overlay/i)
    }
    // The remedy deliberately names the one Argo knob the user can turn;
    // it must still avoid KubeAtlas's internal vocabulary. KubeAtlas's own Settings
    // are the host's to name — an embedded host may have no such place.
    for (const copy of [APP_TREE_NO_FINDINGS_NOTICE, APP_TREE_PERSIST_REMEDY, APP_TREE_API_ERROR_NOTICE]) {
      expect(copy).not.toMatch(/appTree|Tier|overlay|Settings/i)
    }
    expect(APP_TREE_PERSIST_REMEDY).toContain('controller.resource.health.persist')
  })

  test('hasKubeAtlasFinding reads the rows the markers read', () => {
    const row = (health: string, healthSource?: string) =>
      ({ ref: { kind: 'Deployment', namespace: 'p', name: 'w' }, category: 'Unknown', health, healthSource, hasDesired: false, hasLive: true, partial: false }) as const
    expect(hasKubeAtlasFinding(undefined)).toBe(false)
    expect(hasKubeAtlasFinding([row('Degraded', 'controller')])).toBe(false)
    expect(hasKubeAtlasFinding([row('Healthy', 'kubeatlas')])).toBe(false)
    expect(hasKubeAtlasFinding([row('Degraded', 'kubeatlas')])).toBe(true)
    expect(hasKubeAtlasFinding([row('Missing', 'kubeatlas')])).toBe(true)
  })
})

describe('healthSourceNoticeKind', () => {
  test('remote destination wins over appTree; Flux notices only a remote target', () => {
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'appTree', remoteDestination: true })).toBe('remote')
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'appTree', health: 'Degraded' })).toBe('appTree')
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'appTree' })).toBe('appTree')
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'appTree', health: 'Healthy' })).toBeNull()
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'appTree', health: 'Degraded', resourceHealthFromApi: true })).toBeNull()
    expect(healthSourceNoticeKind({ tool: 'argocd', resourceHealthMode: 'inline' })).toBeNull()
    expect(healthSourceNoticeKind({ tool: 'argocd' })).toBeNull()
    expect(healthSourceNoticeKind({ tool: 'fluxcd', resourceHealthMode: 'appTree' })).toBeNull()
    expect(healthSourceNoticeKind({ tool: 'fluxcd', remoteDestination: true })).toBe('remote')
    expect(healthSourceNoticeKind(undefined)).toBeNull()
  })
})
