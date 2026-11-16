import type { SearchMatchedField } from '../../api/client'

// contentMatchLabel names where a hit matched when every token landed in the
// object's content rather than its name, kind or namespace. Trivy Operator
// reports keep their findings under `report`, not a spec.
export function contentMatchLabel(matched: SearchMatchedField[] | undefined): string {
  if (!matched?.length || !matched.every((m) => m.site.startsWith('content:'))) return ''
  return matched.every((m) => m.site.startsWith('content:report.')) ? '命中发现项' : '命中 spec'
}

// partialSearchNotices explain a result the server says is incomplete, so a
// missing CVE doesn't read as "not affected". One line per cause; empty when
// the search was whole.
export function partialSearchNotices(data: { incomplete_findings?: string; access_incomplete?: boolean } | undefined): string[] {
  const out: string[] = []
  if (data?.access_incomplete) {
    out.push('无法确认你是否有权访问部分命名空间中的扫描报告，因此这些报告未纳入搜索结果。请重新搜索以检查更多内容。')
  }
  if (data?.incomplete_findings) {
    out.push('搜索在检查完所有扫描报告的发现项前已超时。可使用 ns: 缩小范围后再检查其余内容。')
  }
  return out
}
