import { describe, expect, it } from 'vitest'
import {
  calculatePeakCompute,
  intervalsToSlots,
  minutesToTimeString,
  parseSchedule,
  serializeSchedule,
  slotsToIntervals,
  timeStringToMinutes,
} from './schedule-config'

describe('schedule-config', () => {
  it('parses empty and default schedule correctly', () => {
    expect(parseSchedule('')).toEqual({ enabled: false, days: {} })
    expect(parseSchedule('{}')).toEqual({ enabled: false, days: {} })
    expect(serializeSchedule({ enabled: false })).toBe('{}')
  })

  it('converts between time string and minutes correctly', () => {
    expect(timeStringToMinutes('08:30')).toBe(510)
    expect(timeStringToMinutes('24:00')).toBe(1440)
    expect(minutesToTimeString(510)).toBe('08:30')
    expect(minutesToTimeString(1440)).toBe('24:00')
  })

  it('converts intervals to slots and back without loss', () => {
    const original = [
      { start: '08:00', end: '12:00' },
      { start: '14:00', end: '18:00' },
    ]
    const slots = intervalsToSlots(original)
    expect(slots[16]).toBe(true) // 08:00 (16 * 30 min)
    expect(slots[23]).toBe(true) // 11:30
    expect(slots[24]).toBe(false) // 12:00
    const reconstructed = slotsToIntervals(slots)
    expect(reconstructed).toEqual(original)
  })

  it('calculates peak compute points using sweep-line correctly', () => {
    const schedDay = {
      enabled: true,
      days: { monday: [{ start: '08:00', end: '18:00' }] },
    }
    const schedNight = {
      enabled: true,
      days: { monday: [{ start: '18:00', end: '24:00' }] },
    }
    const schedOverlap = {
      enabled: true,
      days: { monday: [{ start: '12:00', end: '20:00' }] },
    }

    // Handover without overlap: peak should be 600
    expect(
      calculatePeakCompute([
        { points: 600, schedule: schedDay },
        { points: 600, schedule: schedNight },
      ])
    ).toBe(600)

    // Overlap: peak should be 1200
    expect(
      calculatePeakCompute([
        { points: 600, schedule: schedDay },
        { points: 600, schedule: schedOverlap },
      ])
    ).toBe(1200)

    // Scheduled + 24/7 base: peak should be 400 + 600 = 1000
    expect(
      calculatePeakCompute([
        { points: 400, schedule: { enabled: false } },
        { points: 600, schedule: schedDay },
        { points: 500, schedule: schedNight },
      ])
    ).toBe(1000)
  })
})
