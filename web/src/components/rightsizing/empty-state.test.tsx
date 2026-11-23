import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ScanEmptyState } from './RightsizingScanView'

const props = {
  counts: { increase: 0, reduction: 0, review: 0, need_data: 0, in_range: 2 },
  classFilter: 'actions' as const,
  hasResultFilters: false,
  onlySystemRowsAreHidden: false,
  hasQueryErrors: false,
  onSelect: () => {},
  onClear: () => {},
  onIncludeSystem: () => {},
}

describe('rightsizing empty journeys', () => {
  it('affirms no actionable changes only for evaluated evidence, with an inspection action', () => {
    const html = renderToStaticMarkup(<ScanEmptyState {...props} />)
    expect(html).toContain('未发现需要调整的请求')
    expect(html).toContain('已评估的 2 个容器')
    expect(html).toContain('结论适用于本次扫描范围')
    expect(html).toContain('查看已评估的容器')
  })

  it.each([false, true])(
    'explains unknown evidence (query errors: %s) without claiming health',
    (hasQueryErrors) => {
      const html = renderToStaticMarkup(
        <ScanEmptyState
          {...props}
          counts={{ ...props.counts, in_range: 0, need_data: 2 }}
          hasQueryErrors={hasQueryErrors}
        />,
      )
      expect(html).toContain('数据不足，暂无法给出建议')
      expect(html).toContain('查看数据不足的容器')
      expect(html).toContain(
        hasQueryErrors ? '部分指标查询失败' : '可用的 CPU 和内存历史数据不足',
      )
      expect(html).not.toContain('未发现需要调整的请求')
    },
  )

  it('qualifies a mixture of in-range and unknown containers', () => {
    const html = renderToStaticMarkup(
      <ScanEmptyState {...props} counts={{ ...props.counts, need_data: 1 }} />,
    )
    expect(html).toContain('现有数据中未发现需要调整的请求')
    expect(html).toContain('评估尚未覆盖全部容器')
    expect(html).toContain('查看数据不足的容器')
  })

  it('does not blame missing history when a search or category filter hides results', () => {
    for (const override of [{ hasResultFilters: true }, { classFilter: 'increase' as const }]) {
      const html = renderToStaticMarkup(<ScanEmptyState {...props} {...override} />)
      expect(html).toContain('当前筛选条件下没有匹配结果')
      expect(html).toContain('清除筛选条件')
    }
  })

  it('retains the system-workload recovery action', () => {
    const html = renderToStaticMarkup(<ScanEmptyState {...props} onlySystemRowsAreHidden />)
    expect(html).toContain('系统工作负载已隐藏')
    expect(html).toContain('显示系统工作负载')
  })
})
