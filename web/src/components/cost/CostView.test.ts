import { describe, expect, it } from 'vitest'
import { costUnavailableMessage, resourceKindForCostWorkload } from './CostView'
import { costConfigurationAction } from './source'

describe('resourceKindForCostWorkload', () => {
  it('does not link standalone aggregate rows as pods', () => {
    expect(resourceKindForCostWorkload('standalone')).toBeNull()
  })

  it('keeps static pod rows linkable to their mirror pod', () => {
    expect(resourceKindForCostWorkload('staticpod')).toBe('Pod')
  })
})

describe('costUnavailableMessage', () => {
  it('gives actionable Kubecost source and authentication guidance', () => {
    expect(costUnavailableMessage('source_unavailable')).toContain('设置 → 成本')
    expect(costUnavailableMessage('authentication_error')).toContain('API 密钥')
    expect(costUnavailableMessage('configuration_mismatch')).toContain('不适用于此集群')
    expect(costUnavailableMessage('deployment_configuration_error')).toContain('本地启动参数')
    expect(costUnavailableMessage('deployment_configuration_error')).toContain('Helm values')
  })

  it('distinguishes a scoped empty result from missing source data', () => {
    expect(costUnavailableMessage('no_metrics', { namespaceScoped: true })).toBe(
      'No allocation data is visible in the current namespace scope',
    )
    expect(costUnavailableMessage('no_metrics')).toContain('selected source')
  })

  it('does not point embedded deployments at standalone Settings', () => {
    expect(costUnavailableMessage('no_cost_source', { settingsAvailable: false })).toContain('宿主应用')
    expect(costUnavailableMessage('no_cost_source', { settingsAvailable: false })).not.toContain('Settings')
  })
})

describe('costConfigurationAction', () => {
  it('routes missing Prometheus to Metrics and other source failures to Cost', () => {
    expect(costConfigurationAction('no_prometheus')).toEqual({
      section: 'prometheus',
      label: 'Configure metrics',
    })
    expect(costConfigurationAction('no_cost_source')).toEqual({
      section: 'cost',
      label: 'Configure cost source',
    })
  })
})
