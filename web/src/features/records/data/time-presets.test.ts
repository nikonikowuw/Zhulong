import { endOfDay, startOfDay, subDays } from 'date-fns'
import { describe, expect, it } from 'vitest'
import {
  DEFAULT_TIME_PRESET,
  type TimePresetKey,
  computeRangeFromPreset,
  resolveRecordsWindow,
} from './time-presets'

// 固定参考时刻用本地时间构造，断言全部基于 date-fns 的本地日界转换，
// 这样同一份用例在不同 TZ 的机器上都成立。
const reference = new Date(2026, 2, 31, 14, 30, 0)

const FAST_FORWARD_MS = {
  '15m': 15 * 60 * 1000,
  '30m': 30 * 60 * 1000,
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
} as const

const presetCases: { preset: TimePresetKey; start: Date; end: Date }[] = [
  ...(Object.keys(FAST_FORWARD_MS) as (keyof typeof FAST_FORWARD_MS)[]).map(
    (preset) => ({
      preset,
      start: new Date(reference.getTime() - FAST_FORWARD_MS[preset]),
      end: reference,
    })
  ),
  { preset: 'today', start: startOfDay(reference), end: endOfDay(reference) },
  {
    preset: '7d',
    start: startOfDay(subDays(reference, 6)),
    end: endOfDay(reference),
  },
  {
    preset: '30d',
    start: startOfDay(subDays(reference, 29)),
    end: endOfDay(reference),
  },
]

describe('computeRangeFromPreset', () => {
  it.each(presetCases)(
    'maps $preset onto an ISO window',
    ({ preset, start, end }) => {
      expect(computeRangeFromPreset(preset, reference)).toEqual({
        startTime: start.toISOString(),
        endTime: end.toISOString(),
      })
    }
  )
})

describe('resolveRecordsWindow', () => {
  it('defaults to today when the URL carries no time state', () => {
    expect(DEFAULT_TIME_PRESET).toBe('today')
    expect(resolveRecordsWindow({}, reference)).toEqual({
      preset: 'today',
      startTime: startOfDay(reference).toISOString(),
      endTime: endOfDay(reference).toISOString(),
      isDefaultWindow: true,
    })
  })

  it.each(presetCases)(
    'computes $preset against the reference time',
    ({ preset, start, end }) => {
      expect(resolveRecordsWindow({ preset }, reference)).toEqual({
        preset,
        startTime: start.toISOString(),
        endTime: end.toISOString(),
        // 只有「今日全天」算默认窗口，其余预设都要让「重置筛选」可见
        isDefaultWindow: preset === DEFAULT_TIME_PRESET,
      })
    }
  )

  it('passes a custom absolute range through without a preset label', () => {
    const custom = {
      startTime: '2026-03-01T00:00:00.000Z',
      endTime: '2026-03-02T00:00:00.000Z',
    }
    expect(resolveRecordsWindow(custom, reference)).toEqual({
      ...custom,
      isDefaultWindow: false,
    })
  })

  it('prefers explicit times over a stale preset from a legacy URL', () => {
    // 旧链接可能同时带 preset 与冻结的时间戳：标签必须跟着真实窗口走，
    // 否则会出现「今日全天」配昨天数据的矛盾展示。
    const resolved = resolveRecordsWindow(
      {
        preset: 'today',
        startTime: '2026-03-01T00:00:00.000Z',
        endTime: '2026-03-02T00:00:00.000Z',
      },
      reference
    )
    expect(resolved.preset).toBeUndefined()
    expect(resolved.startTime).toBe('2026-03-01T00:00:00.000Z')
    expect(resolved.endTime).toBe('2026-03-02T00:00:00.000Z')
    expect(resolved.isDefaultWindow).toBe(false)
  })
})
