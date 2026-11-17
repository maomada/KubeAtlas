import type { FeatureCapabilities, KubeAtlasUpgradeRequirement } from '@maomada/k8s-ui'
import { compareVersions } from '../utils/version'

// KubeAtlas Hub embeds the newest @maomada/kubeatlas-app against whatever KubeAtlas is
// installed in each cluster, so these endpoints can be missing on a connected
// KubeAtlas. A KubeAtlas that advertises the feature's `features` flag is always
// trusted; otherwise the release that first served the endpoint decides.
//
// Adding an optional endpoint: advertise a flag for it in
// FeatureCapabilities (internal/k8s/capabilities.go) in the same change, add
// an entry here with that flag and `flagShippedWithEndpoint: true`, and guard
// its hook with useKubeAtlasFeature. TestFeatureFlagsHaveFrontendGates keeps the
// two lists in step. `minimumVersion` can wait for the release to be cut:
// until then the note asks for the latest KubeAtlas.
export interface KubeAtlasFeatureSpec {
  /** Plural noun phrase; the upgrade note reads "<label> need KubeAtlas vX or newer." */
  label: string
  /** First release that served the endpoint, once known. */
  minimumVersion?: string
  flag?: keyof FeatureCapabilities
  /**
   * The flag shipped in the same release as the endpoint, so a KubeAtlas whose
   * capabilities omit it is too old regardless of its version. False for the
   * entries below, whose flags were added after their endpoints.
   */
  flagShippedWithEndpoint?: boolean
}

export const KUBEATLAS_FEATURES = {
  resourceReadCoverage: { label: 'Resource read coverage', flag: 'resourceReadCoverage', flagShippedWithEndpoint: true },
  searchCoverage: { label: 'Search coverage and object filters', flag: 'searchCoverage', flagShippedWithEndpoint: true },
  resourceIssues: { label: 'Operational issues', minimumVersion: 'v1.8.0', flag: 'resourceIssues' },
  resourceIssueCoverage: { label: 'Resource-specific issue views', flag: 'resourceIssueCoverage', flagShippedWithEndpoint: true },
  podEnvironment: { label: 'Values from ConfigMaps and Secrets', minimumVersion: 'v1.9.0', flag: 'podEnvironment' },
  policyResource: { label: 'Policy results', minimumVersion: 'v1.10.0', flag: 'policyResource' },
  capacity: { label: 'Capacity views', minimumVersion: 'v1.9.0' },
  upgradeReadiness: { label: 'Upgrade impact reports', minimumVersion: 'v1.9.0' },
  drainPlan: { label: 'Drain plans', minimumVersion: 'v1.14.0' },
  applications: { label: 'Applications', minimumVersion: 'v1.7.7' },
  workloadHistory: { label: 'Workload history', flag: 'workloadHistory', flagShippedWithEndpoint: true },
  cnpgWorkspace: { label: 'CloudNativePG views', flag: 'cnpgWorkspace', flagShippedWithEndpoint: true },
  cnpgProtectionSetup: { label: 'CloudNativePG backup setup', flag: 'cnpgProtectionSetup', flagShippedWithEndpoint: true },
  gitopsWriteEvidence: { label: 'GitOps revert warnings', flag: 'gitopsWriteEvidence', flagShippedWithEndpoint: true },
  serviceEndpointSlices: { label: 'Service EndpointSlice inventories', flag: 'serviceEndpointSlices', flagShippedWithEndpoint: true },
  trafficRecords: { label: 'Flow records for a graph selection', flag: 'trafficRecords', flagShippedWithEndpoint: true },
  helmSourceIdentity: { label: 'Helm source selection', flag: 'helmSourceIdentity', flagShippedWithEndpoint: true },
} as const satisfies Record<string, KubeAtlasFeatureSpec>

export type KubeAtlasFeature = keyof typeof KUBEATLAS_FEATURES

export type KubeAtlasFeatureSupport = 'supported' | 'unsupported' | 'unknown'

export interface KubeAtlasVersions {
  currentVersion?: string
  latestVersion?: string
}

// Only a plain release can be placed against a minimum. Dev, dirty and
// pre-release builds stay unknown so a custom build is never wrongly gated.
const RELEASE_VERSION = /^v?\d+\.\d+\.\d+$/

/**
 * `capabilities` is the /api/capabilities answer, or undefined while it is
 * still loading; only a loaded answer can prove a flag is absent.
 */
export function kubeatlasSpecSupport(
  spec: KubeAtlasFeatureSpec,
  capabilities: { features?: FeatureCapabilities } | undefined,
  currentVersion: string | undefined,
): KubeAtlasFeatureSupport {
  if (spec.flag && capabilities?.features?.[spec.flag] === true) return 'supported'
  // Unflagged entries all predate the first flag added for this table, so a
  // KubeAtlas that advertises it serves them, whatever version the host reported.
  if (!spec.flag && capabilities?.features?.resourceIssues === true) return 'supported'
  if (spec.flag && spec.flagShippedWithEndpoint && capabilities) return 'unsupported'
  const version = currentVersion?.trim()
  if (!spec.minimumVersion || !version || !RELEASE_VERSION.test(version)) return 'unknown'
  const order = compareVersions(version, spec.minimumVersion)
  if (order === null) return 'unknown'
  return order < 0 ? 'unsupported' : 'supported'
}

export function kubeatlasFeatureSupport(
  feature: KubeAtlasFeature,
  capabilities: { features?: FeatureCapabilities } | undefined,
  currentVersion: string | undefined,
): KubeAtlasFeatureSupport {
  return kubeatlasSpecSupport(KUBEATLAS_FEATURES[feature], capabilities, currentVersion)
}

export function kubeatlasUpgradeRequirement(feature: KubeAtlasFeature, versions: KubeAtlasVersions): KubeAtlasUpgradeRequirement {
  const spec: KubeAtlasFeatureSpec = KUBEATLAS_FEATURES[feature]
  return {
    feature: spec.label,
    ...(spec.minimumVersion ? { minimumVersion: spec.minimumVersion } : {}),
    ...(versions.currentVersion ? { currentVersion: versions.currentVersion } : {}),
    ...(versions.latestVersion ? { latestVersion: versions.latestVersion } : {}),
  }
}

/**
 * The connected KubeAtlas predates `feature`. Carries `kubeatlasUpgrade` so k8s-ui
 * components can render the upgrade note without importing this class.
 */
export class KubeAtlasFeatureUnsupportedError extends Error {
  readonly feature: KubeAtlasFeature
  readonly kubeatlasUpgrade: KubeAtlasUpgradeRequirement

  constructor(feature: KubeAtlasFeature, versions: KubeAtlasVersions) {
    const requirement = kubeatlasUpgradeRequirement(feature, versions)
    super(`${requirement.feature} need a newer KubeAtlas`)
    this.name = 'KubeAtlasFeatureUnsupportedError'
    this.feature = feature
    this.kubeatlasUpgrade = requirement
  }
}

export function isKubeAtlasFeatureUnsupported(error: unknown, feature?: KubeAtlasFeature): boolean {
  return error instanceof KubeAtlasFeatureUnsupportedError && (feature === undefined || error.feature === feature)
}

/**
 * Runs `request` unless the connected KubeAtlas is known to predate `feature`.
 * When its version is unknown, chi's unknown-route 404 (ApiError.unknownRoute)
 * is read as the same answer; on a KubeAtlas known to serve the feature that 404
 * is a routing failure and stays an error.
 */
export async function guardKubeAtlasFeature<T>(
  feature: KubeAtlasFeature,
  support: KubeAtlasFeatureSupport,
  versions: KubeAtlasVersions,
  request: () => Promise<T>,
): Promise<T> {
  if (support === 'unsupported') throw new KubeAtlasFeatureUnsupportedError(feature, versions)
  try {
    return await request()
  } catch (error) {
    if (support === 'unknown' && (error as { unknownRoute?: unknown } | null)?.unknownRoute === true) {
      throw new KubeAtlasFeatureUnsupportedError(feature, versions)
    }
    throw error
  }
}

/**
 * Query retry policy: an unsupported feature or a client error (other than a
 * timeout or rate limit) gives the same answer on every attempt, so retrying
 * only repeats the request against the cluster.
 */
export function shouldRetryKubeAtlasQuery(failureCount: number, error: unknown): boolean {
  if (error instanceof KubeAtlasFeatureUnsupportedError) return false
  const status = (error as { status?: unknown } | null)?.status
  if (typeof status === 'number' && status >= 400 && status < 500 && status !== 408 && status !== 429) {
    return false
  }
  return failureCount < 1
}
