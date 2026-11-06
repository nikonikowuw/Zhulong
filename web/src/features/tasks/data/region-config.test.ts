import { describe, it, expect } from 'vitest'
import { CATEGORY_HUES } from '@/lib/category-palette'
import {
  cropEqual,
  cropRectSchema,
  emptyDraft,
  fromDraft,
  parseCrop,
  parseRegionConfig,
  serializeRegionConfig,
  toDraft,
  regionConfigSchema,
  rectSchema,
  REGION_CATEGORY_HUE,
  type Rect,
  type RegionConfig,
} from './region-config'

describe('region-config data parsing and validation', () => {
  it('validates a valid rect [x1, y1, x2, y2]', () => {
    const valid: Rect = [0.1, 0.2, 0.5, 0.6]
    expect(rectSchema.safeParse(valid).success).toBe(true)
  })

  it('rejects inverted or too small rects', () => {
    // x2 <= x1
    expect(rectSchema.safeParse([0.5, 0.2, 0.5, 0.6]).success).toBe(false)
    // side < 0.01
    expect(rectSchema.safeParse([0.1, 0.2, 0.105, 0.6]).success).toBe(false)
    // out of bounds [0, 1]
    expect(rectSchema.safeParse([-0.1, 0.2, 0.5, 0.6]).success).toBe(false)
    expect(rectSchema.safeParse([0.1, 0.2, 1.2, 0.6]).success).toBe(false)
  })

  it('parses empty or malformed strings gracefully', () => {
    expect(parseRegionConfig('')).toEqual({ roi: [], mask: [] })
    expect(parseRegionConfig('{}')).toEqual({ roi: [], mask: [] })
    expect(parseRegionConfig('invalid-json')).toEqual({ roi: [], mask: [] })
    expect(parseRegionConfig(null)).toEqual({ roi: [], mask: [] })
  })

  it('parses and serializes valid region configs', () => {
    const cfg = {
      roi: [[0.1, 0.1, 0.5, 0.5]] as Rect[],
      mask: [[0.2, 0.2, 0.3, 0.3]] as Rect[],
    }
    const json = serializeRegionConfig(cfg)
    expect(parseRegionConfig(json)).toEqual(cfg)
  })

  it('serializes empty config as {}', () => {
    expect(serializeRegionConfig({ roi: [], mask: [] })).toBe('{}')
  })

  it('enforces maximum 8 rects per category in schema', () => {
    const nineRects: Rect[] = Array.from({ length: 9 }, (_, i) => [
      0.01 * i,
      0.01 * i,
      0.01 * i + 0.05,
      0.01 * i + 0.05,
    ])
    expect(
      regionConfigSchema.safeParse({ roi: nineRects, mask: [] }).success
    ).toBe(false)
  })
})

describe('region-config editor draft round-trip', () => {
  const cfg: RegionConfig = {
    roi: [[0.1, 0.1, 0.5, 0.5]],
    mask: [[0.2, 0.2, 0.3, 0.3]],
  }

  it('assigns a stable distinct id per rect and round-trips back to the wire contract', () => {
    let seq = 0
    const draft = toDraft(cfg, () => `id-${++seq}`)

    expect(draft.roi[0].id).toBe('id-1')
    expect(draft.mask[0].id).toBe('id-2')
    // ids 不得泄漏进持久化契约
    expect(fromDraft(draft)).toEqual(cfg)
  })

  it('preserves ids across an edit so React keys stay stable', () => {
    let seq = 0
    const draft = toDraft(cfg, () => `id-${++seq}`)
    const edited = {
      ...draft,
      roi: [{ ...draft.roi[0], rect: [0.2, 0.2, 0.6, 0.6] as Rect }],
    }
    expect(edited.roi[0].id).toBe(draft.roi[0].id)
    expect(fromDraft(edited).roi[0]).toEqual([0.2, 0.2, 0.6, 0.6])
  })

  it('returns a fresh empty draft each call so callers cannot share mutable state', () => {
    const a = emptyDraft()
    const b = emptyDraft()
    expect(a).not.toBe(b)
    expect(a).toEqual(b)
  })

  it('enforces the stricter minimum side for crop windows', () => {
    // ROI 的 0.01 边长合法，但同一个矩形不能当 crop 用：逆映射会把误差放大 100 倍。
    expect(rectSchema.safeParse([0.1, 0.1, 0.11, 0.5]).success).toBe(true)
    expect(cropRectSchema.safeParse([0.1, 0.1, 0.11, 0.5]).success).toBe(false)
    expect(cropRectSchema.safeParse([0.1, 0.1, 0.15, 0.5]).success).toBe(true)
  })

  it('folds a full-frame crop back to null', () => {
    expect(parseCrop([0, 0, 1, 1])).toBeNull()
    expect(parseCrop([0.1, 0.1, 0.9, 0.9])).toEqual([0.1, 0.1, 0.9, 0.9])
    // 坏数据按“不裁剪”处理，不让整个任务解析失败
    expect(parseCrop([0.5, 0.5, 0.4, 0.4])).toBeNull()
    expect(parseCrop(null)).toBeNull()
  })

  it('treats null and the full frame as the same crop', () => {
    expect(cropEqual(null, [0, 0, 1, 1])).toBe(true)
    expect(cropEqual([0.1, 0.1, 0.5, 0.5], [0.1, 0.1, 0.5, 0.5])).toBe(true)
    expect(cropEqual([0.1, 0.1, 0.5, 0.5], [0.1, 0.1, 0.6, 0.5])).toBe(false)
  })

  it('maps each region category onto a distinct valid category hue', () => {
    // theme-and-i18n §1.3.6：分类色相只能取自有序槽位表，且不得占用状态色相
    expect(CATEGORY_HUES).toContain(REGION_CATEGORY_HUE.roi)
    expect(CATEGORY_HUES).toContain(REGION_CATEGORY_HUE.mask)
    expect(REGION_CATEGORY_HUE.roi).not.toBe(REGION_CATEGORY_HUE.mask)
  })
})

describe('tripwire configuration', () => {
  const line = {
    id: 'entry-1',
    name: '入口',
    points: [
      [0.2, 0.5],
      [0.8, 0.5],
    ],
    direction: 'a_to_b',
  }
  it('preserves persisted identity and direction through editor round trips', () => {
    const config = regionConfigSchema.parse({ tripwires: [line] })
    const draft = toDraft(config, () => 'temporary-rect-id')
    expect(
      parseRegionConfig(serializeRegionConfig(fromDraft(draft))).tripwires
    ).toEqual([line])
  })
  it('rejects degenerate lines, invalid directions, duplicate IDs and malformed points', () => {
    for (const tripwires of [
      [line, { ...line, name: 'duplicate' }],
      [
        {
          ...line,
          points: [
            [0.2, 0.5],
            [0.2, 0.5],
          ],
        },
      ],
      [{ ...line, direction: 'forward' }],
      [
        {
          ...line,
          points: [
            [0, 0, 0],
            [1, 1],
          ],
        },
      ],
      [
        {
          ...line,
          points: [
            [0, Number.NaN],
            [1, 1],
          ],
        },
      ],
    ])
      expect(regionConfigSchema.safeParse({ tripwires }).success).toBe(false)
  })
})
