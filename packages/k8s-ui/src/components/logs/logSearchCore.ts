export type LogSearchRange = [start: number, end: number]

export interface LogSearchResult {
  matchIndices: number[]
  ranges: Record<number, LogSearchRange[]>
}

const MAX_RANGES_PER_LINE = 1000
const MAX_RANGES_PER_REQUEST = 100000
const RANGE_LIMIT_ERROR = '正则搜索匹配结果超出资源限制，请缩小日志范围或简化表达式'

export function searchLogLines(
  lines: string[],
  query: string,
  isCaseSensitive: boolean,
): LogSearchResult {
  const regex = new RegExp(query, isCaseSensitive ? 'g' : 'gi')
  const matchIndices: number[] = []
  const ranges: Record<number, LogSearchRange[]> = {}
  let totalRanges = 0

  lines.forEach((line, index) => {
    regex.lastIndex = 0
    const lineRanges: LogSearchRange[] = []
    let matched = false

    while (true) {
      const match = regex.exec(line)
      if (!match) break
      matched = true
      const start = match.index
      const end = start + match[0].length
      if (start < end) {
        if (lineRanges.length >= MAX_RANGES_PER_LINE || totalRanges >= MAX_RANGES_PER_REQUEST) {
          throw new Error(RANGE_LIMIT_ERROR)
        }
        lineRanges.push([start, end])
        totalRanges++
      }
      if (match[0].length === 0) {
        regex.lastIndex = start + 1
      }
    }

    if (matched) {
      matchIndices.push(index)
      ranges[index] = lineRanges
    }
  })

  return { matchIndices, ranges }
}
