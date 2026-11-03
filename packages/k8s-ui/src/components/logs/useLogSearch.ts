import { useState, useMemo, useCallback, useDeferredValue, useEffect, useRef } from 'react'
import type { VirtuosoHandle } from 'react-virtuoso'
import type { LogEntry } from './useLogBuffer'
import {
  type LogSearchRange,
  type LogSearchResult,
} from './logSearchCore'
import type {
  LogSearchWorkerRequest,
  LogSearchWorkerResponse,
} from './logSearch.worker'
import { stripAnsi, escapeRegExp } from '../../utils/log-format'

export type LogSearchMode = 'highlight' | 'only' | 'hide'

export interface LogSearchWorker {
  onmessage: ((event: MessageEvent<LogSearchWorkerResponse>) => void) | null
  onerror: ((event: ErrorEvent) => void) | null
  postMessage(message: LogSearchWorkerRequest): void
  terminate(): void
}

export type LogSearchWorkerFactory = () => LogSearchWorker

interface UseLogSearchReturn {
  query: string
  setQuery: (q: string) => void
  isRegex: boolean
  toggleRegex: () => void
  setIsRegex: (v: boolean) => void
  isCaseSensitive: boolean
  toggleCaseSensitive: () => void
  /** highlight: show every line, mark matches · only: show matching lines · hide: drop matching lines */
  mode: LogSearchMode
  setMode: (mode: LogSearchMode) => void
  /** Whether the query currently narrows the visible lines (only/hide with a non-empty, valid query) */
  isFiltering: boolean
  isSearching: boolean
  matchCount: number
  currentMatch: number
  /** Indices into the entries array that match */
  matchIndices: number[]
  /** Regex match ranges keyed by entry index in the input entries array. */
  ranges: Record<number, LogSearchRange[]>
  /** The lines left after applying the mode: matching lines for `only`, non-matching for `hide` */
  filteredEntries: LogEntry[]
  /** Error message when regex search cannot be completed */
  regexError: string | null
  goToNext: () => void
  goToPrev: () => void
  isOpen: boolean
  open: () => void
  close: () => void
}

/**
 * The lines a search mode leaves visible. Only and Hide act on whole records:
 * a match on any line of a stack trace keeps or hides the trace together.
 */
export function applySearchMode<T extends { id: number }>(
  entries: readonly T[],
  matchIndices: readonly number[],
  mode: LogSearchMode,
  recordIdOf: (entry: T) => number = e => e.id,
): T[] {
  if (mode === 'highlight') return [...entries]
  const matchedRecords = new Set(matchIndices.map(i => recordIdOf(entries[i])))
  const keepMatches = mode === 'only'
  return entries.filter(e => matchedRecords.has(recordIdOf(e)) === keepMatches)
}

function createLogSearchWorker(): LogSearchWorker {
  if (typeof Worker === 'undefined') {
    throw new Error('Worker unavailable')
  }
  return new Worker(new URL('./logSearch.worker.ts', import.meta.url), { type: 'module' })
}

const EMPTY_RESULT: LogSearchResult = { matchIndices: [], ranges: {} }
const EMPTY_ENTRIES: LogEntry[] = []
const TIMEOUT_ERROR = '正则搜索超时，已取消；请简化表达式或缩小日志范围'
const UNAVAILABLE_ERROR = '当前环境不支持隔离正则搜索'
const WORKER_ERROR = '正则搜索失败，请简化表达式或缩小日志范围'

interface LogSearchCriteria {
  readonly query: string
  readonly isRegex: boolean
  readonly isCaseSensitive: boolean
  readonly workerFactory: LogSearchWorkerFactory
}

interface LogSearchSnapshot {
  readonly criteria: LogSearchCriteria | null
  readonly entries: LogEntry[]
  readonly result: LogSearchResult
  readonly error: string | null
}

interface ActiveLogSearchJob {
  id: number
  criteria: LogSearchCriteria
  entries: LogEntry[]
  worker: LogSearchWorker
  timeout: ReturnType<typeof setTimeout> | null
  settled: boolean
}

const EMPTY_SNAPSHOT: LogSearchSnapshot = {
  criteria: null,
  entries: EMPTY_ENTRIES,
  result: EMPTY_RESULT,
  error: null,
}

function sameCriteria(left: LogSearchCriteria | null, right: LogSearchCriteria): boolean {
  return left !== null &&
    left.query === right.query &&
    left.isRegex === right.isRegex &&
    left.isCaseSensitive === right.isCaseSensitive &&
    left.workerFactory === right.workerFactory
}

export function useLogSearch(
  entries: LogEntry[],
  virtuosoRef: React.RefObject<VirtuosoHandle | null>,
  /** Id of the record an entry belongs to (its first line's id); defaults to the entry itself */
  recordIdOf?: (entry: LogEntry) => number,
  onNavigate?: () => void,
  workerFactory: LogSearchWorkerFactory = createLogSearchWorker,
): UseLogSearchReturn {
  const [query, setQuery] = useState('')
  const [isRegex, setIsRegex] = useState(false)
  const [isCaseSensitive, setIsCaseSensitive] = useState(false)
  const [mode, setMode] = useState<LogSearchMode>('highlight')
  const [currentMatch, setCurrentMatch] = useState(0)
  const [isOpen, setIsOpen] = useState(false)
  const [snapshot, setSnapshot] = useState(EMPTY_SNAPSHOT)
  const activeJob = useRef<ActiveLogSearchJob | null>(null)
  const requestId = useRef(0)

  // Deferring keeps the search input responsive while large log buffers are scanned.
  const deferredQuery = useDeferredValue(query)
  const criteria = useMemo(
    () => ({ query, isRegex, isCaseSensitive, workerFactory }),
    [query, isRegex, isCaseSensitive, workerFactory],
  )

  const literalSearch = useMemo(() => {
    if (!deferredQuery || isRegex) {
      return { matchIndices: [] as number[], regexError: null }
    }

    try {
      const pattern = new RegExp(escapeRegExp(deferredQuery), isCaseSensitive ? 'g' : 'gi')
      const indices: number[] = []
      for (let i = 0; i < entries.length; i++) {
        const plain = stripAnsi(entries[i].content)
        if (pattern.test(plain)) {
          indices.push(i)
        }
        pattern.lastIndex = 0
      }
      return { matchIndices: indices, regexError: null }
    } catch (e) {
      return { matchIndices: [] as number[], regexError: e instanceof Error ? e.message : 'Invalid regex' }
    }
  }, [entries, deferredQuery, isRegex, isCaseSensitive])

  const cancelActiveJob = useCallback(() => {
    const job = activeJob.current
    if (!job) return
    activeJob.current = null
    job.settled = true
    requestId.current++
    if (job.timeout !== null) clearTimeout(job.timeout)
    job.worker.terminate()
  }, [])

  useEffect(() => {
    return cancelActiveJob
  }, [query, isRegex, isCaseSensitive, workerFactory, cancelActiveJob])

  useEffect(() => {
    if (!isRegex || !query || query !== deferredQuery || activeJob.current) return
    const criteriaMatches = sameCriteria(snapshot.criteria, criteria)
    if (criteriaMatches && (snapshot.error || snapshot.entries === entries)) return

    try {
      new RegExp(query, isCaseSensitive ? 'g' : 'gi')
    } catch (error) {
      setSnapshot({
        criteria,
        entries,
        result: EMPTY_RESULT,
        error: error instanceof Error ? error.message : 'Invalid regex',
      })
      return
    }

    let worker: LogSearchWorker
    try {
      worker = workerFactory()
    } catch {
      setSnapshot({ criteria, entries, result: EMPTY_RESULT, error: UNAVAILABLE_ERROR })
      return
    }

    const id = ++requestId.current
    const job: ActiveLogSearchJob = {
      id,
      criteria,
      entries,
      worker,
      timeout: null,
      settled: false,
    }
    const finish = (result: LogSearchResult, error: string | null) => {
      if (job.settled || activeJob.current !== job || requestId.current !== id) return
      job.settled = true
      activeJob.current = null
      if (job.timeout !== null) clearTimeout(job.timeout)
      worker.terminate()
      setSnapshot({ criteria: job.criteria, entries: job.entries, result, error })
    }

    job.timeout = setTimeout(() => {
      finish(EMPTY_RESULT, TIMEOUT_ERROR)
    }, 2000)
    activeJob.current = job

    worker.onmessage = event => {
      if (event.data.id !== id) return
      if ('error' in event.data) {
        finish(EMPTY_RESULT, event.data.error)
      } else {
        finish(event.data.result, null)
      }
    }
    worker.onerror = () => {
      finish(EMPTY_RESULT, WORKER_ERROR)
    }
    try {
      worker.postMessage({
        id,
        lines: entries.map(entry => stripAnsi(entry.content)),
        query,
        isCaseSensitive,
      })
    } catch {
      finish(EMPTY_RESULT, WORKER_ERROR)
    }
  }, [entries, query, deferredQuery, isRegex, isCaseSensitive, workerFactory, criteria, snapshot])

  const snapshotCriteriaMatches = sameCriteria(snapshot.criteria, criteria)
  const regexResultMatches = isRegex &&
    !!query &&
    query === deferredQuery &&
    snapshotCriteriaMatches &&
    snapshot.entries === entries
  const regexError = isRegex && query && snapshotCriteriaMatches ? snapshot.error : null
  const isSearching = isRegex && !!query && !regexError && !regexResultMatches
  const matchIndices = !query
    ? EMPTY_RESULT.matchIndices
    : isRegex
      ? (regexResultMatches ? snapshot.result.matchIndices : EMPTY_RESULT.matchIndices)
      : literalSearch.matchIndices
  const ranges = regexResultMatches ? snapshot.result.ranges : EMPTY_RESULT.ranges

  // Gate on the live query too, so clearing or closing search unfilters immediately
  // rather than after the deferred value catches up.
  const isFiltering = mode !== 'highlight' && !!query && !!deferredQuery && !regexError && !isSearching
  const filteredEntries = useMemo(
    () => (isFiltering ? applySearchMode(entries, matchIndices, mode, recordIdOf) : entries),
    [entries, isFiltering, mode, matchIndices, recordIdOf],
  )
  const filteredIndexById = useMemo(() => {
    if (mode !== 'only' || !isFiltering) return null
    return new Map(filteredEntries.map((e, i) => [e.id, i]))
  }, [mode, isFiltering, filteredEntries])

  // Reset current match when search criteria change (but not when new entries arrive during streaming).
  const prevCriteria = useRef({ query, isRegex, isCaseSensitive })
  useEffect(() => {
    if (
      prevCriteria.current.query !== query ||
      prevCriteria.current.isRegex !== isRegex ||
      prevCriteria.current.isCaseSensitive !== isCaseSensitive
    ) {
      setCurrentMatch(0)
      prevCriteria.current = { query, isRegex, isCaseSensitive }
    }
  }, [query, isRegex, isCaseSensitive])

  const scrollToMatch = useCallback((matchIdx: number) => {
    if (matchIdx < 0 || matchIdx >= matchIndices.length) return
    if (mode === 'hide') return
    onNavigate?.()
    if (mode === 'only') {
      // The list holds whole matching records, so find the matched line within it
      const index = filteredIndexById?.get(entries[matchIndices[matchIdx]].id)
      if (index === undefined) return
      virtuosoRef.current?.scrollToIndex({
        index,
        align: 'center',
        behavior: 'smooth',
      })
    } else {
      const entryIndex = matchIndices[matchIdx]
      virtuosoRef.current?.scrollToIndex({
        index: entryIndex,
        align: 'center',
        behavior: 'smooth',
      })
    }
  }, [entries, matchIndices, mode, filteredIndexById, virtuosoRef, onNavigate])

  const goToNext = useCallback(() => {
    if (matchIndices.length === 0 || mode === 'hide') return
    const next = (currentMatch + 1) % matchIndices.length
    setCurrentMatch(next)
    scrollToMatch(next)
  }, [currentMatch, matchIndices.length, mode, scrollToMatch])

  const goToPrev = useCallback(() => {
    if (matchIndices.length === 0 || mode === 'hide') return
    const prev = (currentMatch - 1 + matchIndices.length) % matchIndices.length
    setCurrentMatch(prev)
    scrollToMatch(prev)
  }, [currentMatch, matchIndices.length, mode, scrollToMatch])

  const toggleRegex = useCallback(() => setIsRegex(p => !p), [])
  const toggleCaseSensitive = useCallback(() => setIsCaseSensitive(p => !p), [])

  const open = useCallback(() => setIsOpen(true), [])
  const close = useCallback(() => {
    cancelActiveJob()
    setIsOpen(false)
    setQuery('')
  }, [cancelActiveJob])

  return {
    query,
    setQuery,
    isRegex,
    toggleRegex,
    setIsRegex,
    isCaseSensitive,
    toggleCaseSensitive,
    mode,
    setMode,
    isFiltering,
    isSearching,
    matchCount: matchIndices.length,
    currentMatch,
    matchIndices,
    ranges,
    filteredEntries,
    regexError,
    goToNext,
    goToPrev,
    isOpen,
    open,
    close,
  }
}
