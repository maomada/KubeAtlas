import { createContext, useContext } from 'react'
import type { KubeAtlasUpgradeRequirement } from '@maomada/k8s-ui'

/** What the user is missing and which KubeAtlas serves it; see KubeAtlasUpgradeRequirement. */
export type KubeAtlasUpgradeRequest = KubeAtlasUpgradeRequirement

/**
 * What an embedding host knows about the connected KubeAtlas and how it upgrades
 * it: KubeAtlasApp's `kubeatlasVersion` and `onRequestKubeAtlasUpgrade` props. Kept apart
 * from KubeAtlasUpgrade.tsx so api/client.ts can read it without an import cycle.
 */
export interface KubeAtlasUpgradeHost {
  kubeatlasVersion?: string
  onRequestKubeAtlasUpgrade?: (request: KubeAtlasUpgradeRequest) => void
}

const KubeAtlasUpgradeHostContext = createContext<KubeAtlasUpgradeHost>({})

export const KubeAtlasUpgradeHostProvider = KubeAtlasUpgradeHostContext.Provider

export function useKubeAtlasUpgradeHost(): KubeAtlasUpgradeHost {
  return useContext(KubeAtlasUpgradeHostContext)
}
