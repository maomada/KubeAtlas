// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { AuditAlerts, type AuditFinding } from './AuditAlerts'
import { AuditCard } from './AuditCard'
import { AuditFindingsTable } from './AuditFindingsTable'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const findings: AuditFinding[] = [
  {
    kind: 'Deployment',
    namespace: 'prod',
    name: 'api',
    checkID: 'privileged',
    category: 'Security',
    severity: 'danger',
    message: 'Container is privileged',
  },
  {
    kind: 'Deployment',
    namespace: 'prod',
    name: 'api',
    checkID: 'cpuRequestMissing',
    category: 'Efficiency',
    severity: 'warning',
    message: 'CPU request is missing',
  },
]

describe('legacy audit severity presentation', () => {
  it('labels raw summary counts as High and Medium', () => {
    const html = renderToString(
      <AuditCard
        data={{
          passing: 3,
          danger: 1,
          warning: 2,
          categories: {
            Security: { passing: 1, danger: 1, warning: 0 },
            Efficiency: { passing: 2, danger: 0, warning: 2 },
          },
        }}
        onNavigate={() => {}}
      />,
    )
    expect(html).toMatch(/1(?:<!-- -->)? 高风险/)
    expect(html).toMatch(/2(?:<!-- -->)? 中风险/)
    expect(html).toContain('bg-orange-500/10')
    expect(html).not.toContain('critical')
  })

  it('uses localized severity labels in resource audit summaries', () => {
    const html = renderToString(<AuditAlerts findings={findings} />)
    expect(html).toMatch(/1(?:<!-- -->)? 高风险/)
    expect(html).toMatch(/1(?:<!-- -->)? 中风险/)
    expect(html).not.toContain('critical')
  })

  it('renders localized audit chrome while preserving finding data', async () => {
    const host = document.createElement('div')
    const root = createRoot(host)
    const onViewAll = vi.fn()
    try {
      await act(async () => root.render(<AuditAlerts findings={findings} onViewAll={onViewAll} />))
      expect(host.textContent).toContain('审计发现')
      expect(host.textContent).toContain('1 高风险')
      expect(host.textContent).toContain('1 中风险')

      const disclosure = host.querySelector<HTMLButtonElement>('[aria-expanded]')!
      await act(async () => disclosure.click())
      expect(host.textContent).toContain('查看全部发现')
      expect(host.textContent).toContain('Container is privileged')
      expect(host.textContent).toContain('Security')

      const viewAll = [...host.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.includes('查看全部发现'))!
      await act(async () => viewAll.click())
      expect(onViewAll).toHaveBeenCalledTimes(1)
    } finally {
      await act(async () => root.unmount())
    }
  })

  it('uses High and Medium in audit totals and filters', () => {
    const html = renderToString(<AuditFindingsTable findings={findings} />)
    expect(html).toContain('High')
    expect(html).toContain('Medium')
    expect(html).not.toContain('Critical')
    expect(html).not.toContain('Warning')
  })
})
