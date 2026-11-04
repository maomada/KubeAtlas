import type { CostDataSource, CostUnavailableReason } from '../../api/client'
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

export function costSourceLabel(source?: CostDataSource): string {
  return costSourcePreferenceLabel(source === 'kubecost' ? 'kubecost' : 'prometheus')
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

export function costRateLabels(window?: string): {
  hourly: string
  rate: string
  allocationTitle: string
  allocationPeriod: string
} {
  if (window === '1d') {
    return {
      hourly: 'Hourly (1-day average)',
      rate: '1-day average hourly rate',
      allocationTitle: '1-day average allocation and use',
      allocationPeriod: 'Allocation and observed use: 1-day average',
    }
  }
  if (window === '1h') {
    return {
      hourly: 'Hourly (1-hour average)',
      rate: '1-hour average hourly rate',
      allocationTitle: '1-hour average allocation and use',
      allocationPeriod: 'Allocation and observed use: 1-hour average',
    }
  }
  return {
    hourly: 'Current hourly',
    rate: 'current hourly rate',
    allocationTitle: 'Current allocation and use',
    allocationPeriod: 'Allocation and CPU use: 1h average · Memory use: current',
  }
}

export function costFreshnessLabel(
  source?: CostDataSource,
  window?: string,
  dataThrough?: string,
): string {
  if (source !== 'kubecost') return window ? `last ${window} average` : 'current allocation average'
  const average = window === '1d'
    ? '1-day allocation average'
    : window === '1h'
      ? '1-hour allocation average'
      : 'current allocation average'
  if (!dataThrough) return `latest Kubecost ${average}`
  const timestamp = costDataThroughLabel(dataThrough)
  if (!timestamp) return `latest Kubecost ${average}`
  return `Kubecost ${average} · data through ${timestamp}`
}

export function costDataThroughLabel(dataThrough?: string): string {
  if (!dataThrough) return ''
  const timestamp = new Date(dataThrough)
  if (Number.isNaN(timestamp.getTime())) return ''
  return timestamp.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
}

export function costIntegrationUnavailableMessage(
  reason?: CostUnavailableReason | 'load_error',
  settingsAvailable = true,
): string | undefined {
  switch (reason) {
    case 'query_error':
      return settingsAvailable
        ? '所选成本来源无法提供数据。请在“设置 → 指标”（OpenCost）或“设置 → 成本”（Kubecost）中检查连接。'
        : '所选成本来源无法提供数据。请联系运维人员检查连接和查询状态。'
    case 'no_cost_source':
      return settingsAvailable
        ? '未找到兼容的成本来源。请在“设置 → 指标”中连接 OpenCost 指标，或在“设置 → 成本”中配置 Kubecost。'
        : '此集群未配置兼容的成本来源。请在宿主应用或 KubeAtlas 部署中配置 OpenCost 指标或 Kubecost。'
    case 'source_unavailable':
      return settingsAvailable
        ? 'Kubecost Aggregator 不可用。请在“设置 → 成本”中检查 URL、网络路径和集群 ID。'
        : 'Kubecost Aggregator 不可用。请在宿主应用或 KubeAtlas 部署中更新此集群的成本来源配置。'
    case 'metrics_settings_error':
      return '加载成本数据前需要检查此集群保存的指标设置。请在“设置 → 指标”中检查。'
    case 'cost_settings_error':
      return '加载成本数据前需要检查此集群保存的成本设置。请在“设置 → 成本”中检查。'
    case 'deployment_configuration_error':
      return 'KubeAtlas 的运维配置成本或指标配置无效。请检查本地启动参数、部署环境变量和 Helm values。'
    case 'authentication_error':
      return settingsAvailable
        ? 'Kubecost 拒绝了配置的 API 密钥。请在“设置 → 成本”中更新。'
        : 'Kubecost 拒绝了配置的 API 密钥。请在宿主应用或 KubeAtlas 部署中更新此集群凭据。'
    case 'configuration_mismatch':
      return settingsAvailable
        ? '保存的 Kubecost 设置不适用于此集群。请在“设置 → 成本”中更新集群 ID 或本地 API 密钥。'
        : 'Kubecost 设置不适用于此集群。请在宿主应用或 KubeAtlas 部署中更新集群 ID 或凭据。'
    default:
      return undefined
  }
}
