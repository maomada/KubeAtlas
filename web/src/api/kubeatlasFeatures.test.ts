import { describe, expect, it, vi } from 'vitest'
import { getKubeAtlasUpgradeRequirement } from '@maomada/k8s-ui'
import { ApiError } from './client'
import {
  KUBEATLAS_FEATURES,
  KubeAtlasFeatureUnsupportedError,
  guardKubeAtlasFeature,
  isKubeAtlasFeatureUnsupported,
  kubeatlasFeatureSupport,
  kubeatlasSpecSupport,
  type KubeAtlasFeatureSpec,
  shouldRetryKubeAtlasQuery,
} from './kubeatlasFeatures'

describe('kubeatlasFeatureSupport', () => {
  it('trusts the advertised flag over the version', () => {
    expect(kubeatlasFeatureSupport('policyResource', { features: { policyResource: true } }, 'v1.7.2')).toBe('supported')
    expect(kubeatlasFeatureSupport('policyResource', { features: { policyResource: true } }, undefined)).toBe('supported')
  })

  it('gates a legacy agent that predates the flag by its release version', () => {
    expect(kubeatlasFeatureSupport('resourceIssues', undefined, 'v1.7.2')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('resourceIssues', undefined, 'v1.8.0')).toBe('supported')
    expect(kubeatlasFeatureSupport('podEnvironment', { features: {} }, '1.8.7')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('podEnvironment', { features: {} }, '1.9.0')).toBe('supported')
    // v1.10 advertised a features block, just not this flag.
    expect(kubeatlasFeatureSupport('policyResource', { features: { yamlReview: true } }, 'v1.9.2')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('policyResource', { features: { yamlReview: true } }, 'v1.12.0')).toBe('supported')
  })

  it('never gates on a version it cannot place', () => {
    for (const version of [undefined, '', 'dev', 'unknown', 'v1.7.2-dirty', 'v1.7.2-3-gdeadbee', 'v1.10.0-rc.1', 'v1.7']) {
      expect(kubeatlasFeatureSupport('policyResource', undefined, version)).toBe('unknown')
    }
  })

  it('gates features without a flag on version alone', () => {
    expect(kubeatlasFeatureSupport('drainPlan', { features: { yamlReview: true } }, 'v1.13.1')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('drainPlan', undefined, 'v1.14.0')).toBe('supported')
    expect(kubeatlasFeatureSupport('capacity', undefined, 'dev')).toBe('unknown')
    expect(kubeatlasFeatureSupport('applications', undefined, 'v1.7.6')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('applications', undefined, 'v1.7.7')).toBe('supported')
  })

  it('keeps the unflagged entries to those that predate the first flag', () => {
    // kubeatlasSpecSupport treats them as served by any KubeAtlas advertising
    // resourceIssues; a new entry must ship its own flag instead.
    const unflagged = Object.entries(KUBEATLAS_FEATURES).filter(([, spec]) => !('flag' in spec)).map(([name]) => name)
    expect(unflagged.sort()).toEqual(['applications', 'capacity', 'drainPlan', 'upgradeReadiness'])
  })

  it('lifts a stale host version for unflagged features once any table flag is advertised', () => {
    expect(kubeatlasFeatureSupport('drainPlan', { features: { resourceIssues: true } }, 'v1.7.2')).toBe('supported')
    expect(kubeatlasFeatureSupport('capacity', { features: { resourceIssues: true } }, 'v1.7.2')).toBe('supported')
    expect(kubeatlasFeatureSupport('upgradeReadiness', { features: { resourceIssues: true } }, undefined)).toBe('supported')
  })
})

describe('workload history, whose flag shipped with its endpoint', () => {
  it('trusts a loaded capabilities answer, and probes before it arrives', () => {
    expect(kubeatlasFeatureSupport('workloadHistory', { features: { workloadHistory: true } }, undefined)).toBe('supported')
    expect(kubeatlasFeatureSupport('workloadHistory', { features: { policyResource: true } }, 'v1.15.0')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('workloadHistory', { features: {} }, 'dev')).toBe('unsupported')
    expect(kubeatlasFeatureSupport('workloadHistory', undefined, 'v1.15.0')).toBe('unknown')
  })
})

describe('kubeatlasSpecSupport for a feature whose flag shipped with its endpoint', () => {
  const driftAlerts: KubeAtlasFeatureSpec = { label: 'Drift alerts', flag: 'resourceIssues', flagShippedWithEndpoint: true }

  it('reads a loaded answer without the flag as too old, whatever the version', () => {
    expect(kubeatlasSpecSupport(driftAlerts, { features: {} }, 'dev')).toBe('unsupported')
    expect(kubeatlasSpecSupport(driftAlerts, {}, 'v1.7.2')).toBe('unsupported')
    expect(kubeatlasSpecSupport(driftAlerts, { features: { resourceIssues: true } }, 'v1.7.2')).toBe('supported')
  })

  it('waits for /api/capabilities before deciding, and never version-gates without a first release', () => {
    expect(kubeatlasSpecSupport(driftAlerts, undefined, 'v1.7.2')).toBe('unknown')
  })
})

describe('guardKubeAtlasFeature', () => {
  const versions = { currentVersion: 'v1.7.2', latestVersion: 'v1.15.0' }

  it('never asks a KubeAtlas known to be too old', async () => {
    const request = vi.fn(() => Promise.resolve('data'))
    await expect(guardKubeAtlasFeature('policyResource', 'unsupported', versions, request))
      .rejects.toBeInstanceOf(KubeAtlasFeatureUnsupportedError)
    expect(request).not.toHaveBeenCalled()
  })

  it("reads the router's unknown-route 404 as unsupported", async () => {
    const error = await guardKubeAtlasFeature('policyResource', 'unknown', versions, () =>
      Promise.reject(new ApiError('HTTP 404 (Not Found)', 404, {}, { unknownRoute: true })),
    ).catch((e: unknown) => e)
    expect(isKubeAtlasFeatureUnsupported(error, 'policyResource')).toBe(true)
    expect(getKubeAtlasUpgradeRequirement(error)).toEqual({
      feature: 'Policy results',
      minimumVersion: 'v1.10.0',
      currentVersion: 'v1.7.2',
      latestVersion: 'v1.15.0',
    })
  })

  it('passes every other failure through untouched', async () => {
    const handler404 = new ApiError('pods "web" not found', 404, { error: 'pods "web" not found' })
    await expect(guardKubeAtlasFeature('policyResource', 'unknown', versions, () => Promise.reject(handler404)))
      .rejects.toBe(handler404)
    const hubNotFound = new ApiError('HTTP 404 (Not Found)', 404)
    await expect(guardKubeAtlasFeature('policyResource', 'supported', versions, () => Promise.reject(hubNotFound)))
      .rejects.toBe(hubNotFound)
    expect(getKubeAtlasUpgradeRequirement(hubNotFound)).toBeNull()
  })

  it("keeps the router's 404 an error on a KubeAtlas known to serve the feature", async () => {
    const routing = new ApiError('HTTP 404 (Not Found)', 404, {}, { unknownRoute: true })
    const error = await guardKubeAtlasFeature('policyResource', 'supported', versions, () => Promise.reject(routing))
      .catch((e: unknown) => e)
    expect(error).toBe(routing)
    expect(getKubeAtlasUpgradeRequirement(error)).toBeNull()
  })

  it('returns the data when the request succeeds', async () => {
    await expect(guardKubeAtlasFeature('policyResource', 'unknown', {}, () => Promise.resolve(42))).resolves.toBe(42)
  })
})

describe('shouldRetryKubeAtlasQuery', () => {
  it('does not retry an unsupported feature or a settled client error', () => {
    expect(shouldRetryKubeAtlasQuery(0, new KubeAtlasFeatureUnsupportedError('resourceIssues', {}))).toBe(false)
    for (const status of [400, 401, 403, 404, 409]) {
      expect(shouldRetryKubeAtlasQuery(0, new ApiError('x', status))).toBe(false)
    }
  })

  it('retries timeouts, rate limits, server errors and network failures once', () => {
    for (const error of [new ApiError('x', 408), new ApiError('x', 429), new ApiError('x', 503), new TypeError('Failed to fetch')]) {
      expect(shouldRetryKubeAtlasQuery(0, error)).toBe(true)
      expect(shouldRetryKubeAtlasQuery(1, error)).toBe(false)
    }
  })
})
