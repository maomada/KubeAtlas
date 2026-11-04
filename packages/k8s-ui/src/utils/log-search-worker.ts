import type { LogSearchResult } from '../components/logs/logSearchCore'
import type { LogSearchWorkerRequest, LogSearchWorkerResponse } from '../components/logs/logSearch.worker'

export interface LogSearchWorker {
  onmessage: ((event: MessageEvent<LogSearchWorkerResponse>) => void) | null
  onerror: ((event: ErrorEvent) => void) | null
  onmessageerror?: ((event: MessageEvent) => void) | null
  postMessage(message: LogSearchWorkerRequest): void
  terminate(): void
}

export type LogSearchWorkerFactory = () => LogSearchWorker

export function createLogSearchWorker(): LogSearchWorker {
  if (typeof Worker === 'undefined') {
    throw new Error('当前环境不支持隔离正则搜索')
  }
  return new Worker(new URL('../components/logs/logSearch.worker.ts', import.meta.url), { type: 'module' })
}

let requestId = 0

export function runLogRegexSearch(
  lines: string[], query: string, isCaseSensitive: boolean, signal?: AbortSignal,
): Promise<LogSearchResult> {
  return new Promise((resolve, reject) => {
    const abortError = () => new DOMException('正则搜索已取消', 'AbortError')
    if (signal?.aborted) {
      reject(abortError())
      return
    }

    let worker: LogSearchWorker
    try {
      worker = createLogSearchWorker()
    } catch {
      reject(new Error('当前环境不支持隔离正则搜索'))
      return
    }

    const id = ++requestId
    let settled = false
    const finish = (result: LogSearchResult | null, error?: Error) => {
      if (settled) return
      settled = true
      clearTimeout(timeout)
      signal?.removeEventListener('abort', abort)
      worker.onmessage = null
      worker.onerror = null
      worker.onmessageerror = null
      worker.terminate()
      if (error) reject(error)
      else resolve(result!)
    }
    const abort = () => finish(null, abortError())
    const timeout = setTimeout(() => {
      finish(null, new Error('正则搜索超时，已取消；请简化表达式或缩小日志范围'))
    }, 2000)

    worker.onmessage = event => {
      if (event.data.id !== id) return
      if ('error' in event.data) finish(null, new Error(event.data.error))
      else finish(event.data.result)
    }
    const fail = () => finish(null, new Error('正则搜索失败，请简化表达式或缩小日志范围'))
    worker.onerror = fail
    worker.onmessageerror = fail
    signal?.addEventListener('abort', abort, { once: true })
    try {
      worker.postMessage({ id, lines, query, isCaseSensitive })
    } catch {
      fail()
    }
  })
}
