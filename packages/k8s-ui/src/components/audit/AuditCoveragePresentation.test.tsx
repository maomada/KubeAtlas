import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { AuditCard } from './AuditCard'
import { ChecksView } from '../checks/ChecksView'
import { FilterLocationProvider } from '../../filter-state'

describe('audit coverage presentation', () => {
  it('localizes known category labels and preserves unknown category names', () => {
    const html = renderToString(
      <AuditCard
        data={{
          passing: 3,
          missingInputs: [],
          warning: 1,
          danger: 0,
          categories: {
            Efficiency: { passing: 1, warning: 1, danger: 0 },
            Reliability: { passing: 1, warning: 0, danger: 0 },
            Security: { passing: 1, warning: 0, danger: 0 },
            FutureCategory: { passing: 0, warning: 0, danger: 0 },
          },
        }}
        onNavigate={() => {}}
      />,
    )
    expect(html).toContain('效率')
    expect(html).toContain('可靠性')
    expect(html).toContain('安全性')
    expect(html).toContain('FutureCategory')
  })

  for (const [passing, missingInputs, expected] of [
    [0, [], '没有评估任何资源'],
    [0, ['secrets'], '部分检查无法运行'],
    [4, ['secrets'], '部分检查无法运行'],
    [4, [], '全部检查通过'],
  ] as const) {
    it(`shows ${expected} with ${passing} passing and ${missingInputs.length} unavailable inputs`, () => {
      const html = renderToString(<AuditCard data={{ passing, missingInputs: [...missingInputs], warning: 0, danger: 0, categories: {} }} onNavigate={() => {}} />)
      expect(html).toContain(expected)
      if (missingInputs.length || passing === 0) expect(html).not.toContain('全部检查通过')
    })
  }
  it('keeps a coverage warning alongside real findings', () => {
    const html = renderToString(<AuditCard data={{ passing: 1, missingInputs: ['secrets'], warning: 1, danger: 0, categories: { Security: { passing: 1, warning: 0, danger: 0 } } }} onNavigate={() => {}} />)
    expect(html).toContain('部分检查无法运行')
    expect(html).not.toContain('全部检查通过')
  })
  for (const [evaluated, missingInputs, expected] of [
    [0, [], '没有评估资源'],
    [0, ['secrets'], '可用数据中没有发现项'],
    [4, ['secrets'], '可用数据中没有发现项'],
    [4, [], '所有已审计资源均通过检查。'],
  ] as const) {
    it(`Checks: ${expected}`, () => {
      const html = renderToString(
        <FilterLocationProvider value={{ searchParams: new URLSearchParams(), update: () => {} }}>
          <ChecksView checks={[]} catalog={{}} anyData evaluated={evaluated} missingInputs={[...missingInputs]} />
        </FilterLocationProvider>,
      )
      expect(html).toContain(expected)
      if (missingInputs.length) {
        expect(html).toContain('不可用的输入（')
        expect(html).toContain('Secret')
        expect(html).not.toContain('所有已审计资源均通过检查。')
      }
    })
  }
})

describe('replica placement coverage explanation', () => {
  for (const inputs of [['replicasets'], ['replicaset-ownership'], ['replicasets', 'replicaset-ownership', 'secrets']]) {
    it(`names the unevaluated check for ${inputs.join(', ')}`, () => {
      const html = renderToString(
        <FilterLocationProvider value={{ searchParams: new URLSearchParams(), update: () => {} }}>
          <ChecksView checks={[]} catalog={{}} anyData evaluated={0} missingInputs={inputs} />
        </FilterLocationProvider>,
      )
      expect(html).toContain('同一节点上的副本')
      expect(html).toContain('无法完整评估')
      expect(html).toContain('跳过了受影响的 Deployment，而非判定为通过')
      expect(html).toContain('可用数据中没有发现项')
      expect(html).not.toContain('replicaset-ownership')
      if (inputs.includes('replicasets')) expect(html).toContain('ReplicaSet')
      if (inputs.includes('replicaset-ownership')) expect(html).toContain('ReplicaSet 所有权')
    })
  }
  it('does not imply missing replica evidence for unrelated unavailable inputs', () => {
    const html = renderToString(
      <FilterLocationProvider value={{ searchParams: new URLSearchParams(), update: () => {} }}>
        <ChecksView checks={[]} catalog={{}} anyData evaluated={4} missingInputs={['secrets']} />
      </FilterLocationProvider>,
    )
    expect(html).toContain('不可用的输入（')
    expect(html).toContain('Secret')
    expect(html).not.toContain('同一节点上的副本')
    expect(html).not.toContain('跳过了受影响的 Deployment，而非判定为通过')
  })
})


describe('unavailable input details', () => {
  it('keeps a long input list collapsed with readable kinds and preserves unknown reasons', () => {
    const html = renderToString(
      <FilterLocationProvider value={{ searchParams: new URLSearchParams(), update: () => {} }}>
        <ChecksView checks={[]} catalog={{}} anyData evaluated={0}
          missingInputs={['serviceaccounts', 'horizontalpodautoscalers', 'limitranges', 'poddisruptionbudgets', 'configmap-references', 'secret-references', 'unrecognized-input']} />
      </FilterLocationProvider>,
    )
    expect(html).toContain('不可用的输入（7）')
    expect(html).toContain('aria-expanded="false"')
    expect(html).toContain('ServiceAccount, HorizontalPodAutoscaler, LimitRange, PodDisruptionBudget, ConfigMap 引用, Secret 引用, unrecognized-input')
    expect(html).not.toContain('所有已审计资源均通过检查。')
  })
})
