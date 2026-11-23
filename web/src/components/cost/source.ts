import type { CostDataSource, CostUnavailableReason } from '../../api/client'
import type { Translate } from '@maomada/k8s-ui/hooks/useLocale'
import { previousSettingsNote, type PreviousIntegrationSettings } from '../../hooks/usePreviousIntegrationSettings'

export function isCostConfigurable(reason?: string): boolean {
  return reason === 'no_prometheus' || reason === 'no_cost_source' || reason === 'source_unavailable'
    || reason === 'authentication_error' || reason === 'configuration_mismatch'
    || reason === 'metrics_settings_error' || reason === 'cost_settings_error'
}

export type CostSourcePreference = 'auto' | CostDataSource

export const COST_SOURCE_OPTIONS: { value: CostSourcePreference; label: string; description: string }[] = [
  { value: 'auto', label: '自动', description: '优先使用可用的 OpenCost 指标，否则使用 Kubecost' },
  { value: 'prometheus', label: 'OpenCost 指标', description: '来自指标连接' },
  { value: 'kubecost', label: 'Kubecost', description: '来自 Kubecost Aggregator' },
]

export function costSourcePreferenceLabel(preference?: string): string {
  return COST_SOURCE_OPTIONS.find(option => option.value === (preference || 'auto'))?.label ?? preference ?? '自动'
}

export function costSourceLabel(source?: CostDataSource, t: Translate = zh => zh): string {
  return source === 'kubecost' ? 'Kubecost' : t(costSourcePreferenceLabel('prometheus'), 'OpenCost metrics')
}

export function isCostDiscoveryPending(reason?: string): boolean {
  return reason === 'no_prometheus' || reason === 'no_cost_source'
}

export function costConfigurationAction(reason?: CostUnavailableReason, offers?: PreviousIntegrationSettings): {
  section: 'prometheus' | 'cost'
  label: string
  note?: string
} {
  if (reason === 'metrics_settings_error') return { section: 'prometheus', label: 'Review metrics settings' }
  if (reason === 'cost_settings_error') return { section: 'cost', label: 'Review cost settings' }
  const previousKind = reason === 'no_prometheus'
    ? offers?.metrics ? 'metrics' : offers?.explicitCostBackend ? 'cost' : undefined
    : reason === 'no_cost_source'
      ? offers?.explicitCostBackend ? 'cost' : offers?.metrics ? 'metrics' : undefined
      : isCostConfigurable(reason) && offers?.cost ? 'cost' : undefined
  const offer = previousKind && offers?.[previousKind]
  if (previousKind && offer) return previousKind === 'metrics'
    ? { section: 'prometheus', label: 'Configure metrics', note: previousSettingsNote('metrics', offer) }
    : { section: 'cost', label: 'Configure cost source', note: previousSettingsNote('cost', offer) }
  return reason === 'no_prometheus'
    ? { section: 'prometheus', label: 'Configure metrics' }
    : { section: 'cost', label: 'Configure cost source' }
}

export function costRateLabels(window?: string, t: Translate = (_zh, en) => en): {
  hourly: string
  rate: string
  allocationTitle: string
  allocationPeriod: string
} {
  if (window === '1d') {
    return {
      hourly: t('每小时（1 天均值）', 'Hourly (1-day average)'),
      rate: t('1 天平均小时费率', '1-day average hourly rate'),
      allocationTitle: t('1 天平均分配量和用量', '1-day average allocation and use'),
      allocationPeriod: t('分配量和观测用量 · 1 天均值', 'Allocation and observed use: 1-day average'),
    }
  }
  if (window === '1h') {
    return {
      hourly: t('每小时（1 小时均值）', 'Hourly (1-hour average)'),
      rate: t('1 小时平均费率', '1-hour average hourly rate'),
      allocationTitle: t('1 小时平均分配量和用量', '1-hour average allocation and use'),
      allocationPeriod: t('分配量和观测用量 · 1 小时均值', 'Allocation and observed use: 1-hour average'),
    }
  }
  return {
    hourly: t('当前每小时', 'Current hourly'),
    rate: t('当前小时费率', 'current hourly rate'),
    allocationTitle: t('当前分配量和用量', 'Current allocation and use'),
    allocationPeriod: t('分配量和 CPU 用量 · 1 小时均值。内存用量 · 当前值', 'Allocation and CPU use: 1h average · Memory use: current'),
  }
}

export function costFreshnessLabel(
  source?: CostDataSource,
  window?: string,
  dataThrough?: string,
  t: Translate = (_zh, en) => en,
  locale?: string,
): string {
  if (source !== 'kubecost') return window ? t(`最近 ${window} 的均值`, `last ${window} average`) : t('当前分配均值', 'current allocation average')
  const average = window === '1d'
    ? t('1 天平均分配量', '1-day allocation average')
    : window === '1h'
      ? t('1 小时平均分配量', '1-hour allocation average')
      : t('当前分配均值', 'current allocation average')
  const timestamp = costDataThroughLabel(dataThrough, locale)
  if (!timestamp) return t(`Kubecost 最新${average}`, `latest Kubecost ${average}`)
  return t(`Kubecost ${average} · 数据截至 ${timestamp}`, `Kubecost ${average} · data through ${timestamp}`)
}

export function costDataThroughLabel(dataThrough?: string, locale?: string): string {
  if (!dataThrough) return ''
  const timestamp = new Date(dataThrough)
  if (Number.isNaN(timestamp.getTime())) return ''
  return timestamp.toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' })
}

export function costIntegrationUnavailableMessage(
  reason?: CostUnavailableReason | 'load_error',
  settingsAvailable = true,
  t: Translate = zh => zh,
): string | undefined {
  switch (reason) {
    case 'query_error':
      return settingsAvailable
        ? t('所选成本来源无法提供数据。请在“设置 → 指标”（OpenCost）或“设置 → 成本”（Kubecost）中检查连接。', 'The selected cost source returned no data. Check Settings → Metrics for OpenCost or Settings → Cost for Kubecost.')
        : t('所选成本来源无法提供数据。请联系运维人员检查连接和查询状态。', 'The selected cost source returned no data. Contact your operator to check the connection and query status.')
    case 'no_cost_source':
      return settingsAvailable
        ? t('未找到兼容的成本来源。请在“设置 → 指标”中连接 OpenCost 指标，或在“设置 → 成本”中配置 Kubecost。', 'No compatible cost source was found. Connect OpenCost through Settings → Metrics or configure Kubecost in Settings → Cost.')
        : t('此集群未配置兼容的成本来源。请在宿主应用或 KubeAtlas 部署中配置 OpenCost 指标或 Kubecost。', 'This cluster has no compatible cost source. Configure OpenCost metrics or Kubecost in the host application or KubeAtlas deployment.')
    case 'source_unavailable':
      return settingsAvailable
        ? t('Kubecost Aggregator 不可用。请在“设置 → 成本”中检查 URL、网络路径和集群 ID。', 'Kubecost Aggregator is unavailable. Check the URL, network path, and cluster ID in Settings → Cost.')
        : t('Kubecost Aggregator 不可用。请在宿主应用或 KubeAtlas 部署中更新此集群的成本来源配置。', 'Kubecost Aggregator is unavailable. Update the cluster cost source in the host application or KubeAtlas deployment.')
    case 'metrics_settings_error':
      return t('加载成本数据前需要检查此集群保存的指标设置。请在“设置 → 指标”中检查。', 'Review the saved settings for this cluster under Settings → Metrics before loading cost data.')
    case 'cost_settings_error':
      return t('加载成本数据前需要检查此集群保存的成本设置。请在“设置 → 成本”中检查。', 'Review the saved settings for this cluster under Settings → Cost before loading cost data.')
    case 'deployment_configuration_error':
      return t('KubeAtlas 的运维配置成本或指标配置无效。请检查本地启动参数、部署环境变量和 Helm values。', 'The deployment has invalid cost or metrics settings. Check its startup arguments, environment variables, and Helm values.')
    case 'authentication_error':
      return settingsAvailable
        ? t('Kubecost 拒绝了配置的 API 密钥。请在“设置 → 成本”中更新。', 'Kubecost rejected the configured API key. Update it in Settings → Cost.')
        : t('Kubecost 拒绝了配置的 API 密钥。请在宿主应用或 KubeAtlas 部署中更新此集群凭据。', 'Kubecost rejected the configured API key. Update the cluster credentials in the host application or KubeAtlas deployment.')
    case 'configuration_mismatch':
      return settingsAvailable
        ? t('保存的 Kubecost 设置不适用于此集群。请在“设置 → 成本”中更新集群 ID 或本地 API 密钥。', 'The saved Kubecost settings do not apply to this cluster. Update the cluster ID or API key in Settings → Cost.')
        : t('Kubecost 设置不适用于此集群。请在宿主应用或 KubeAtlas 部署中更新集群 ID 或凭据。', 'The Kubecost settings do not apply to this cluster. Update the cluster ID or credentials in the host application or KubeAtlas deployment.')
    default:
      return undefined
  }
}
