import { describe, expect, it } from 'vitest'
import { formatRecordTime } from './format-time'

describe('formatRecordTime', () => {
  it('returns "-" for undefined, empty, or invalid timestamps', () => {
    expect(formatRecordTime(undefined)).toBe('-')
    expect(formatRecordTime('')).toBe('-')
    expect(formatRecordTime('not-a-date')).toBe('-')
    expect(formatRecordTime('2026-99-99T00:00:00Z')).toBe('-')
  })

  it('formats valid RFC3339 timestamps without throwing', () => {
    const out = formatRecordTime('2026-03-31T10:15:30Z', 'MM-dd HH:mm:ss')
    expect(out).toMatch(/^\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  })
})
