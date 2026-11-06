// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  useLogSearch,
  type LogSearchWorker,
  type LogSearchWorkerFactory,
} from './useLogSearch'
import type { LogSearchResult } from './logSearchCore'
import type { LogSearchWorkerRequest, LogSearchWorkerResponse } from './logSearch.worker'
import type { LogEntry } from './useLogBuffer'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

class FakeWorker implements LogSearchWorker {
  onmessage: ((event: MessageEvent<LogSearchWorkerResponse>) => void) | null = null
  onerror: ((event: ErrorEvent) => void) | null = null
  message: LogSearchWorkerRequest | null = null
  readonly postMessage = vi.fn((message: LogSearchWorkerRequest) => { this.message = message })
  readonly terminate = vi.fn()

  respond(result: LogSearchResult) {
    if (!this.message) throw new Error('worker was not called')
    this.onmessage?.({ data: { id: this.message.id, result } } as MessageEvent<LogSearchWorkerResponse>)
  }

  reject(error: string) {
    if (!this.message) throw new Error('worker was not called')
    this.onmessage?.({ data: { id: this.message.id, error } } as MessageEvent<LogSearchWorkerResponse>)
  }
}

const entries: LogEntry[] = [
  {
    id: 1,
    timestamp: '',
    content: 'alpha',
    container: '',
    level: 'info',
    levelSource: 'none',
    isJson: false,
    isLogfmt: false,
  },
  {
    id: 2,
    timestamp: '',
    content: 'beta',
    container: '',
    level: 'info',
    levelSource: 'none',
    isJson: false,
    isLogfmt: false,
  },
]

let root: Root
let host: HTMLDivElement
let latest: ReturnType<typeof useLogSearch>

function Harness({ workerFactory, value = entries }: { workerFactory?: LogSearchWorkerFactory; value?: LogEntry[] }) {
  latest = useLogSearch(value, { current: null }, undefined, undefined, workerFactory)
  return null
}

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function render(workerFactory: LogSearchWorkerFactory, value = entries) {
  await act(async () => root.render(<Harness workerFactory={workerFactory} value={value} />))
  await act(async () => {
    latest.setIsRegex(true)
    latest.setQuery('a')
  })
}

describe('regex search worker lifecycle', () => {
  it('does not synchronously scan and reports a timeout', async () => {
    vi.useFakeTimers()
    const worker = new FakeWorker()
    await render(() => worker)

    expect(latest.isSearching).toBe(true)
    expect(latest.matchCount).toBe(0)
    expect(worker.postMessage).toHaveBeenCalledOnce()
    await act(async () => latest.setMode('only'))
    expect(latest.isFiltering).toBe(false)
    expect(latest.filteredEntries).toHaveLength(entries.length)

    await act(async () => vi.advanceTimersByTime(2000))
    expect(worker.terminate).toHaveBeenCalled()
    expect(latest.regexError).toContain('正则搜索超时')
  })

  it('terminates superseded work and ignores stale responses', async () => {
    const workers: FakeWorker[] = []
    const factory = () => {
      const worker = new FakeWorker()
      workers.push(worker)
      return worker
    }
    await render(factory)
    const first = workers[0]

    await act(async () => latest.setQuery('b'))
    expect(first.terminate).toHaveBeenCalled()
    await act(async () => first.respond({ matchIndices: [0], ranges: { 0: [[0, 1]] } }))
    expect(latest.matchCount).toBe(0)

    await act(async () => workers[1].respond({ matchIndices: [1], ranges: { 1: [[0, 1]] } }))
    expect(latest.matchIndices).toEqual([1])
  })

  it('coalesces streaming entries without extending the deadline and latches errors', async () => {
    vi.useFakeTimers()
    const workers: FakeWorker[] = []
    const factory = () => {
      const worker = new FakeWorker()
      workers.push(worker)
      return worker
    }
    await render(factory)

    await act(async () => vi.advanceTimersByTime(1000))
    const firstUpdate = [...entries, { ...entries[0], id: 3, content: 'gamma' }]
    await act(async () => root.render(<Harness workerFactory={factory} value={firstUpdate} />))
    const secondUpdate = [...firstUpdate, { ...entries[1], id: 4, content: 'delta' }]
    await act(async () => root.render(<Harness workerFactory={factory} value={secondUpdate} />))
    expect(workers).toHaveLength(1)
    expect(workers[0].terminate).not.toHaveBeenCalled()
    expect(latest.isSearching).toBe(true)
    expect(latest.isFiltering).toBe(false)
    expect(latest.filteredEntries).toHaveLength(secondUpdate.length)

    await act(async () => vi.advanceTimersByTime(1000))
    expect(workers[0].terminate).toHaveBeenCalledOnce()
    expect(latest.regexError).toContain('正则搜索超时')

    const latestEntries = [...secondUpdate, { ...entries[0], id: 5, content: 'epsilon' }]
    await act(async () => root.render(<Harness workerFactory={factory} value={latestEntries} />))
    expect(latest.regexError).toContain('正则搜索超时')
    expect(workers).toHaveLength(1)

    await act(async () => latest.setQuery('b'))
    expect(workers).toHaveLength(2)
    expect(workers[1].message?.lines).toEqual(latestEntries.map(entry => entry.content))
    await act(async () => workers[1].respond({ matchIndices: [1], ranges: { 1: [[0, 1]] } }))
    expect(latest.regexError).toBeNull()
    expect(latest.matchIndices).toEqual([1])
  })

  it('keeps invalid-regex errors and refuses environments without workers', async () => {
    const worker = new FakeWorker()
    const factory = vi.fn(() => worker)
    await act(async () => root.render(<Harness workerFactory={factory} />))
    await act(async () => {
      latest.setIsRegex(true)
      latest.setQuery('[')
    })
    expect(factory).toHaveBeenCalledOnce()
    await act(async () => worker.reject('Invalid regular expression: /[/: Unterminated character class'))
    expect(latest.regexError).toContain('Invalid regular expression')

    vi.stubGlobal('Worker', undefined)
    await act(async () => root.render(<Harness key="unavailable" />))
    await act(async () => {
      latest.setIsRegex(true)
      latest.setQuery('a')
    })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(latest.regexError).toBe('当前环境不支持隔离正则搜索')
    expect(latest.matchCount).toBe(0)
  })
})
