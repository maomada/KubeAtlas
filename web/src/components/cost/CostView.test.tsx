// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { LocaleProvider } from '@maomada/k8s-ui/hooks/useLocale'
import { ConnectionProvider } from '../../context/ConnectionContext'
import { ContextSwitchProvider } from '../../context/ContextSwitchContext'
import { CostView, costUnavailableMessage, resourceKindForCostWorkload } from './CostView'
import { costConfigurationAction } from './source'

describe('CostView language', () => {
  it.each([
    [undefined, 'Cost Insights', 'Rightsizing', '未找到兼容的成本来源', '返回概览'],
    ['zh-CN', '成本分析', '资源优化', '未找到兼容的成本来源', '返回概览'],
    ['en', 'Cost Insights', 'Rightsizing', 'No compatible cost source was found', 'Back to dashboard'],
  ])('preserves the expected cost copy for %s', (locale, heading, tab, message, back) => {
    const client = new QueryClient()
    client.setQueryData(['opencost-summary', []], { available: false, reason: 'no_cost_source' })
    const view = (
      <MemoryRouter initialEntries={['/cost']}>
        <QueryClientProvider client={client}>
          <ConnectionProvider>
            <ContextSwitchProvider>
              <CostView namespaces={[]} onBack={() => {}} />
            </ContextSwitchProvider>
          </ConnectionProvider>
        </QueryClientProvider>
      </MemoryRouter>
    )
    const preference = vi.spyOn(Storage.prototype, 'getItem').mockReturnValue(locale ?? null)
    try {
      const html = renderToStaticMarkup(locale ? <LocaleProvider>{view}</LocaleProvider> : view)
      expect(html).toContain(heading)
      expect(html).toContain(tab)
      expect(html).toContain(message)
      expect(html).toContain(back)
    } finally {
      preference.mockRestore()
      client.clear()
    }
  })
})

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
