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
})
