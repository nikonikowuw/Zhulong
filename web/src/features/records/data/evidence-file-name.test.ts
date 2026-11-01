import { describe, expect, it } from 'vitest'
import { evidenceFileName } from './evidence-file-name'

describe('evidenceFileName', () => {
  it('prefixes the record id with the local capture timestamp', () => {
    // 先用本地时间构造再转 ISO，断言值在所有时区都成立。
    const capturedAt = new Date(2026, 2, 31, 20, 0, 5).toISOString()
    expect(evidenceFileName('01M6BFGRAVWZW52QAYV42H4VJT', capturedAt)).toBe(
      '20260331_200005-01M6BFGRAVWZW52QAYV42H4VJT.jpg'
    )
  })

  it('keeps the lexicographic order equal to the capture order', () => {
    const early = evidenceFileName(
      'cap-a',
      new Date(2026, 2, 31, 9, 0, 0).toISOString()
    )
    const late = evidenceFileName(
      'cap-b',
      new Date(2026, 2, 31, 10, 0, 0).toISOString()
    )
    expect(early < late).toBe(true)
  })

  it('zero-pads every component so month/day/hour do not sort wrong', () => {
    const capturedAt = new Date(2026, 0, 2, 3, 4, 5).toISOString()
    expect(evidenceFileName('cap-1', capturedAt)).toBe(
      '20260102_030405-cap-1.jpg'
    )
  })

  it('falls back to the bare id when capturedAt is unparseable', () => {
    // 宁可丢时间，也不要把 "Invalid Date" 写进用户的下载目录。
    expect(evidenceFileName('cap-1', '')).toBe('cap-1.jpg')
    expect(evidenceFileName('cap-1', 'not-a-date')).toBe('cap-1.jpg')
  })
})
