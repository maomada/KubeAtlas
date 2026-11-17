import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { CheckRemediationBlock, ChecksView } from './ChecksView'
import type { Check, EffectiveCheckFinding } from './types'

function check(messages: string[], cluster = 'one'): Check {
  const findings: EffectiveCheckFinding[] = messages.map((message, i) => ({
    source: 'kubeatlas_builtin',
    resource: { cluster_id: cluster, group: '', kind: 'Secret', namespace: 'test', name: `cert-${i}` },
    checkID: 'tlsCertificateExpiry', category: 'Reliability', originalSeverity: 'danger',
    effectiveSeverity: 'high', message,
    state: { visibility: 'visible', source: 'detector_default', scoreImpact: 'counts', alertImpact: 'alerts', complianceImpact: 'counts' },
  }))
  return {
    id: cluster, source: 'kubeatlas_builtin', subject: findings[0].resource,
    checkID: 'tlsCertificateExpiry', category: 'Reliability', effectiveSeverity: 'high',
    title: 'TLS certificate expiring', message: messages[0], affectedFindings: findings.length,
    affectedResources: findings.length, representativeFinding: findings[0], findings,
  }
}

function render(checks: Check[], options: { anyData?: boolean; missingInputs?: string[] } = {}) {
  return renderToStaticMarkup(<ChecksView checks={checks} catalog={{}} anyData={options.anyData ?? true} missingInputs={options.missingInputs} clusterLabel={(c) => c.subject.cluster_id} />)
}

const deadline = 'TLS certificate expires in 2d (2026-09-22T12:00:00Z)'

it('renders the localized remediation heading', () => {
  const html = renderToStaticMarkup(<CheckRemediationBlock remediation="Rotate the certificate." />)
  expect(html).toContain('修复方法')
  expect(html).toContain('Rotate the certificate.')
})

describe('finding evidence', () => {
  it('localizes check chrome while preserving check titles, messages, kinds, and resource names', () => {
    const html = render([check([deadline])])
    expect(html).toContain('搜索检查…')
    expect(html).toContain('TLS certificate expiring')
    expect(html).toContain('Secret')
    expect(html).toContain('test')
    expect(html).toContain('cert-0')
    expect(html).toContain(deadline)
  })

  it('distinguishes unavailable inputs from the incomplete empty state', () => {
    const html = render([], { anyData: true, missingInputs: ['replicaset-ownership'] })
    expect(html).toContain('部分检查无法运行')
    expect(html).toContain('不可用的输入')
    expect(html).toContain('ReplicaSet 所有权')
    expect(html).toContain('可用数据中没有发现项')
    expect(html).toContain('这不表示所有检查都已通过')
  })

  it.each([1, 2])('preserves common evidence once for %i findings', (count) => {
    expect(render([check(Array(count).fill(deadline))]).split(deadline)).toHaveLength(2)
  })

  it('preserves distinct evidence, including messages that differ only by resource name', () => {
    const messages = ['cert-0 expires tomorrow', 'cert-1 expires tomorrow']
    const html = render([check(messages)])
    for (const message of messages) expect(html).toContain(message)
  })

  it('preserves the nonempty evidence in a mixed empty list', () => {
    expect(render([check(['', deadline])])).toContain(deadline)
    expect(render([check(['', ''])])).not.toContain('whitespace-pre-wrap')
  })

  it('compares hidden findings too instead of applying the first deadline to the whole list', () => {
    const html = render([check([...Array(8).fill(deadline), 'A different deadline'])])
    expect(html.split(deadline)).toHaveLength(9)
    expect(html).toContain('查看全部 9 项')
  })

  it('keeps common evidence within each cluster group', () => {
    const html = render([check([deadline, deadline], 'one'), check(['Different cluster deadline'], 'two')])
    expect(html.split(deadline)).toHaveLength(2)
    expect(html).toContain('Different cluster deadline')
  })
})
