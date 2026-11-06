import { searchLogLines, type LogSearchResult } from './logSearchCore'

export interface LogSearchWorkerRequest {
  id: number
  lines: string[]
  query: string
  isCaseSensitive: boolean
}

export type LogSearchWorkerResponse =
  | { id: number; result: LogSearchResult }
  | { id: number; error: string }

self.onmessage = (event: MessageEvent<LogSearchWorkerRequest>) => {
  const { id, lines, query, isCaseSensitive } = event.data
  try {
    self.postMessage({ id, result: searchLogLines(lines, query, isCaseSensitive) } satisfies LogSearchWorkerResponse)
  } catch (error) {
    self.postMessage({
      id,
      error: error instanceof Error ? error.message : 'Invalid regex',
    } satisfies LogSearchWorkerResponse)
  }
}
