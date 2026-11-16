import { describe, expect, it } from 'vitest'
import { contentMatchLabel, partialSearchNotices } from './omnibar-match'

const site = (s: string) => ({ token: 't', site: s, score: 12 })

describe('contentMatchLabel', () => {
  it('names a match inside Trivy findings', () => {
    expect(contentMatchLabel([site('content:report.vulnerabilities[3]')])).toBe('命中发现项')
  })
  it('keeps the native spec field name for other content', () => {
    expect(contentMatchLabel([site('content:spec.template.spec.containers[0].env[1].value')])).toBe('命中 spec')
    expect(contentMatchLabel([site('content:report.checks[0]'), site('content:spec.x')])).toBe('命中 spec')
  })
  it('is empty when any token matched identity', () => {
    expect(contentMatchLabel([site('content:report.vulnerabilities[0]'), site('name')])).toBe('')
    expect(contentMatchLabel([])).toBe('')
    expect(contentMatchLabel(undefined)).toBe('')
  })
})

describe('partialSearchNotices', () => {
  it('says why each part of the search is missing', () => {
    expect(partialSearchNotices({ access_incomplete: true })).toEqual([
      '无法确认你是否有权访问部分命名空间中的扫描报告，因此这些报告未纳入搜索结果。请重新搜索以检查更多内容。',
    ])
    expect(partialSearchNotices({ incomplete_findings: 'time_limit' })).toEqual([
      '搜索在检查完所有扫描报告的发现项前已超时。可使用 ns: 缩小范围后再检查其余内容。',
    ])
  })
  it('shows both causes when both happened', () => {
    expect(partialSearchNotices({ access_incomplete: true, incomplete_findings: 'time_limit' })).toHaveLength(2)
  })
  it('is empty for a whole result or an older KubeAtlas', () => {
    expect(partialSearchNotices({})).toEqual([])
    expect(partialSearchNotices(undefined)).toEqual([])
  })
})
