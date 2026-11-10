import { describe, expect, it } from 'vitest'
import {
  addIntervalToDay,
  calculatePeakCompute,
  clearDaySchedule,
  copyDaySchedule,
  formatDuration,
  intervalsToSlots,
  mergeTimeRanges,
  minutesToTimeString,
  parseSchedule,
  removeIntervalFromDay,
  serializeSchedule,
  slotsToIntervals,
  timeStringToMinutes,
  updateIntervalInDay,
  WORKDAY_KEYS,
  type WeekSchedule,
} from './schedule-config'

describe('schedule-config', () => {
  it('parses empty and default schedule correctly', () => {
    expect(parseSchedule('')).toEqual({ enabled: false, days: {} })
    expect(parseSchedule('{}')).toEqual({ enabled: false, days: {} })
    expect(
      parseSchedule('{"enabled":true,"days":{"monday":"invalid"}}')
    ).toEqual({ enabled: false, days: {} })
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

  it('copies day schedule to target days correctly', () => {
    const initial: WeekSchedule = {
      enabled: true,
      days: {
        monday: [{ start: '09:00', end: '17:00' }],
      },
    }
    const copied = copyDaySchedule(initial, 'monday', [...WORKDAY_KEYS])
    expect(copied.days?.tuesday).toEqual([{ start: '09:00', end: '17:00' }])
    expect(copied.days?.friday).toEqual([{ start: '09:00', end: '17:00' }])
    expect(copied.days?.saturday).toBeUndefined()
  })

  it('clears day schedule correctly', () => {
    const initial: WeekSchedule = {
      enabled: true,
      days: {
        monday: [{ start: '09:00', end: '17:00' }],
        tuesday: [{ start: '10:00', end: '12:00' }],
      },
    }
    const cleared = clearDaySchedule(initial, 'monday')
    expect(cleared.days?.monday).toBeUndefined()
    expect(cleared.days?.tuesday).toEqual([{ start: '10:00', end: '12:00' }])
  })

  it('adds and merges interval to day correctly', () => {
    const initial: WeekSchedule = {
      enabled: true,
      days: {
        monday: [{ start: '09:00', end: '12:00' }],
      },
    }
    // Add overlapping interval 11:00 - 15:00
    const merged = addIntervalToDay(initial, 'monday', {
      start: '11:00',
      end: '15:00',
    })
    expect(merged.days?.monday).toEqual([{ start: '09:00', end: '15:00' }])
  })

  it('removes interval from day correctly', () => {
    const initial: WeekSchedule = {
      enabled: true,
      days: {
        monday: [
          { start: '09:00', end: '12:00' },
          { start: '14:00', end: '18:00' },
        ],
      },
    }
    const removed = removeIntervalFromDay(initial, 'monday', 0)
    expect(removed.days?.monday).toEqual([{ start: '14:00', end: '18:00' }])
  })

  it('preserves exact minute precision without 30-min quantization', () => {
    const initial: WeekSchedule = {
      enabled: true,
      days: {},
    }
    // Add 08:45 ~ 17:15
    const updated = addIntervalToDay(initial, 'monday', {
      start: '08:45',
      end: '17:15',
    })
    expect(updated.days?.monday).toEqual([{ start: '08:45', end: '17:15' }])

    // Update interval to 09:10 ~ 18:20
    const modified = updateIntervalInDay(updated, 'monday', 0, {
      start: '09:10',
      end: '18:20',
    })
    expect(modified.days?.monday).toEqual([{ start: '09:10', end: '18:20' }])
  })

  it('merges overlapping minute-level intervals accurately', () => {
    const merged = mergeTimeRanges([
      { start: '08:45', end: '12:15' },
      { start: '11:30', end: '17:45' },
    ])
    expect(merged).toEqual([{ start: '08:45', end: '17:45' }])
  })

  it('formats duration correctly with hours or cross-day', () => {
    expect(formatDuration('08:00', '18:00')).toBe('10.0h')
    expect(formatDuration('09:30', '11:00')).toBe('1.5h')
    expect(formatDuration('20:00', '06:00')).toBe('跨天')
    expect(formatDuration('12:00', '12:00')).toBe('跨天')
  })
})
