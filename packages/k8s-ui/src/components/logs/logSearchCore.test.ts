import { describe, expect, it } from 'vitest'
import { searchLogLines } from './logSearchCore'

describe('searchLogLines', () => {
  it('returns ranges and honors case sensitivity', () => {
    expect(searchLogLines(['Alpha alpha', 'ALPHA', 'none'], 'alpha', false)).toEqual({
      matchIndices: [0, 1],
      ranges: {
        0: [[0, 5], [6, 11]],
        1: [[0, 5]],
      },
    })
    expect(searchLogLines(['Alpha alpha', 'ALPHA'], 'alpha', true)).toEqual({
      matchIndices: [0],
      ranges: { 0: [[6, 11]] },
    })
  })

  it('keeps zero-length matches in the match set without emitting empty ranges', () => {
    expect(searchLogLines(['alpha', ''], '^', false)).toEqual({
      matchIndices: [0, 1],
      ranges: { 0: [], 1: [] },
    })
  })

  it('allows exactly 1000 ranges per line and rejects the 1001st', () => {
    expect(searchLogLines(['x'.repeat(1000)], 'x', true).ranges[0]).toHaveLength(1000)
    expect(() => searchLogLines(['x'.repeat(1001)], 'x', true)).toThrow('正则搜索匹配结果超出资源限制')
  })

  it('allows exactly 100000 ranges per request and rejects an additional range', () => {
    const atLimit = Array.from({ length: 100 }, () => 'x'.repeat(1000))
    const overLimit = [...atLimit, 'x']

    const result = searchLogLines(atLimit, 'x', true)
    expect(result.matchIndices).toHaveLength(100)
    expect(Object.values(result.ranges).reduce((total, lineRanges) => total + lineRanges.length, 0)).toBe(100000)
    expect(() => searchLogLines(overLimit, 'x', true)).toThrow('正则搜索匹配结果超出资源限制')
  })
})
