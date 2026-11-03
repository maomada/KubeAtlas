import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { highlightLiteralSearchMatches, highlightSearchMatches } from './log-format'
import type { LogSearchWorker } from './log-search-worker'
import type { LogSearchWorkerRequest, LogSearchWorkerResponse } from '../components/logs/logSearch.worker'
import { searchLogLines } from '../components/logs/logSearchCore'

class FakeWorker implements LogSearchWorker {
  static instances: FakeWorker[] = []
  onmessage: LogSearchWorker['onmessage'] = null
  onerror: LogSearchWorker['onerror'] = null
  onmessageerror: LogSearchWorker['onmessageerror'] = null
  request!: LogSearchWorkerRequest
  postMessage = vi.fn((request: LogSearchWorkerRequest) => { this.request = request })
  terminate = vi.fn()

  constructor() { FakeWorker.instances.push(this) }

  respond() {
    const { id, lines, query, isCaseSensitive } = this.request
    let data: LogSearchWorkerResponse
    try {
      data = { id, result: searchLogLines(lines, query, isCaseSensitive) }
    } catch (error) {
      data = { id, error: (error as Error).message }
    }
    this.onmessage?.({ data } as MessageEvent<LogSearchWorkerResponse>)
  }
}

beforeEach(() => {
  FakeWorker.instances = []
  vi.stubGlobal('Worker', FakeWorker)
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('async log highlighting', () => {
  it('dispatches raw text and regex unchanged, then escapes matched and unmatched text', async () => {
    const pending = highlightSearchMatches('<aa>&AA', '(a)\\1', true, true)
    const worker = FakeWorker.instances[0]
    expect(worker.request).toMatchObject({ lines: ['<aa>&AA'], query: '(a)\\1', isCaseSensitive: true })
    worker.respond()
    await expect(pending).resolves.toBe('&lt;<mark class="log-highlight">aa</mark>&gt;&amp;AA')
    expect(worker.terminate).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps literal-only rendering synchronous without interpreting regex', async () => {
    expect(highlightLiteralSearchMatches('<A+> a+', 'a+')).toBe('&lt;<mark class="log-highlight">A+</mark>&gt; <mark class="log-highlight">a+</mark>')
    await expect(highlightSearchMatches('<A+>', 'a+', false, true)).resolves.toBe('&lt;A+&gt;')
    await expect(highlightSearchMatches('<&>', '')).resolves.toBe('&lt;&amp;&gt;')
    expect(FakeWorker.instances).toHaveLength(0)
  })

  it('terminates timed-out work and ignores a captured late callback', async () => {
    const pending = highlightSearchMatches('a'.repeat(100) + '!', '^(a+)+$', true)
    const worker = FakeWorker.instances[0]
    const late = worker.onmessage!
    const assertion = expect(pending).rejects.toThrow('正则搜索超时')
    await vi.advanceTimersByTimeAsync(2000)
    await assertion
    late({ data: { id: worker.request.id, result: { matchIndices: [], ranges: {} } } } as MessageEvent<LogSearchWorkerResponse>)
    expect(worker.terminate).toHaveBeenCalledOnce()
    expect(worker.onmessage).toBeNull()
  })

  it('aborts active work and starts no Worker for an already aborted request', async () => {
    const controller = new AbortController()
    const pending = highlightSearchMatches('abc', '.', true, false, controller.signal)
    const assertion = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await assertion
    expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce()
    await expect(highlightSearchMatches('abc', '.', true, false, controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(FakeWorker.instances).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('rejects unavailable Workers rather than executing regex on the caller thread', async () => {
    vi.stubGlobal('Worker', undefined)
    await expect(highlightSearchMatches('a'.repeat(100) + '!', '^(a+)+$', true)).rejects.toThrow('不支持隔离正则搜索')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('rejects Worker construction failures without retaining a timeout', async () => {
    let constructionAttempted = false
    class FailingWorker {
      constructor() {
        constructionAttempted = true
        throw new Error('construction failed')
      }
    }
    vi.stubGlobal('Worker', FailingWorker)

    await expect(highlightSearchMatches('abc', '.', true)).rejects.toThrow('不支持隔离正则搜索')
    expect(constructionAttempted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('cleans up when postMessage throws', async () => {
    class ThrowingPostMessageWorker extends FakeWorker {
      postMessage = vi.fn((_request: LogSearchWorkerRequest) => {
        throw new Error('post failed')
      })
    }
    vi.stubGlobal('Worker', ThrowingPostMessageWorker)

    const pending = highlightSearchMatches('abc', '.', true)
    const worker = FakeWorker.instances[0]
    await expect(pending).rejects.toThrow('正则搜索失败')
    expect(worker.postMessage).toHaveBeenCalledOnce()
    expect(worker.terminate).toHaveBeenCalledOnce()
    expect(worker.onmessage).toBeNull()
    expect(worker.onerror).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['[', '.'])('propagates Worker syntax or range-limit errors for %s', async query => {
    const pending = highlightSearchMatches('a'.repeat(1001), query, true)
    const assertion = expect(pending).rejects.toThrow()
    FakeWorker.instances[0].respond()
    await assertion
    expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce()
  })

  it.each(['onerror', 'onmessageerror'] as const)('cleans up after %s', async event => {
    const pending = highlightSearchMatches('abc', '.', true)
    const worker = FakeWorker.instances[0]
    const assertion = expect(pending).rejects.toThrow('正则搜索失败')
    worker[event]?.(undefined as never)
    await assertion
    expect(worker.terminate).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps simultaneous requests independent and ignores mismatched IDs', async () => {
    const first = highlightSearchMatches('abc', 'a', true)
    const second = highlightSearchMatches('ABC', 'a', true)
    const [a, b] = FakeWorker.instances
    a.onmessage?.({ data: { id: b.request.id, error: 'stale' } } as MessageEvent<LogSearchWorkerResponse>)
    expect(a.terminate).not.toHaveBeenCalled()
    b.respond()
    a.respond()
    await expect(first).resolves.toBe('<mark class="log-highlight">a</mark>bc')
    await expect(second).resolves.toBe('<mark class="log-highlight">A</mark>BC')
  })
})
