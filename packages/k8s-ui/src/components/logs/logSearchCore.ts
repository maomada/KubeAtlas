export type LogSearchRange = [start: number, end: number]

export interface LogSearchResult {
  matchIndices: number[]
  ranges: Record<number, LogSearchRange[]>
}

const MAX_RANGES_PER_LINE = 1000

export function searchLogLines(
  lines: string[],
  query: string,
  isCaseSensitive: boolean,
): LogSearchResult {
  const regex = new RegExp(query, isCaseSensitive ? 'g' : 'gi')
  const matchIndices: number[] = []
  const ranges: Record<number, LogSearchRange[]> = {}

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
      if (start < end && lineRanges.length < MAX_RANGES_PER_LINE) {
        lineRanges.push([start, end])
      }
      if (match[0].length === 0) {
        regex.lastIndex = start + 1
      }
      if (lineRanges.length >= MAX_RANGES_PER_LINE) break
    }

    if (matched) {
      matchIndices.push(index)
      ranges[index] = lineRanges
    }
  })

  return { matchIndices, ranges }
}
