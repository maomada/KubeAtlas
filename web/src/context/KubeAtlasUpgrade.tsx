import { useMemo, type ReactNode } from 'react'
import { KubeAtlasUpgradeContext, type KubeAtlasUpgradeActions } from '@maomada/k8s-ui'
import { useCapabilities, useVersionCheck } from '../api/client'
import { IN_CLUSTER_UPGRADE_URL } from '../utils/version'
import { useKubeAtlasUpgradeHost } from './KubeAtlasUpgradeHost'

/**
 * Supplies the "Upgrade KubeAtlas" action to the inline notes k8s-ui renders for
 * features the connected KubeAtlas is too old to serve. A host that owns upgrades
 * (upstream Cloud) gets the request; standalone KubeAtlas links to the instructions
 * for how it was installed.
 */
export function KubeAtlasUpgradeProvider({ children }: { children: ReactNode }) {
  const { onRequestKubeAtlasUpgrade } = useKubeAtlasUpgradeHost()
  const { data: capabilities } = useCapabilities()
  const { data: versionInfo } = useVersionCheck()
  const mode = capabilities?.deployment?.mode ?? 'local'
  // A local binary upgrades from its release page; an in-cluster or Cloud
  // agent from the docs section that covers Helm, Argo CD and Flux installs.
  const upgradeHref = mode === 'local' && versionInfo?.releaseUrl
    ? versionInfo.releaseUrl
    : `${IN_CLUSTER_UPGRADE_URL}#upgrading`

  const value = useMemo<KubeAtlasUpgradeActions>(
    () => (onRequestKubeAtlasUpgrade ? { onRequestUpgrade: onRequestKubeAtlasUpgrade } : { upgradeHref }),
    [onRequestKubeAtlasUpgrade, upgradeHref],
  )

  return <KubeAtlasUpgradeContext.Provider value={value}>{children}</KubeAtlasUpgradeContext.Provider>
}
