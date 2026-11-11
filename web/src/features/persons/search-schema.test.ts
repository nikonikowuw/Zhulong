import { describe, expect, it } from 'vitest'
import { personsSearchSchema } from './search-schema'

describe('personsSearchSchema', () => {
  it('accepts a well-formed search', () => {
    const parsed = personsSearchSchema.parse({
      view: 'grid',
      page: 3,
      pageSize: 20,
      search: 'alice',
      faceStatus: 'missing',
    })
    expect(parsed).toMatchObject({
      view: 'grid',
      page: 3,
      pageSize: 20,
      search: 'alice',
      faceStatus: 'missing',
    })
  })

  it('rejects pageSize=0 so the paginator cannot compute Infinity pages', () => {
    // 回归：`Math.ceil(total / 0)` 是 Infinity，页面会显示「共 Infinity 页」。
    const parsed = personsSearchSchema.parse({ pageSize: 0 })
    expect(parsed.pageSize).toBe(10)
    expect(Number.isFinite(parsed.pageSize!)).toBe(true)
  })

  it('clamps pageSize above the backend maximum', () => {
    expect(personsSearchSchema.parse({ pageSize: 5000 }).pageSize).toBe(10)
  })

  it('clamps page below 1 and above the backend maximum', () => {
    expect(personsSearchSchema.parse({ page: 0 }).page).toBe(1)
    expect(personsSearchSchema.parse({ page: -3 }).page).toBe(1)
    expect(personsSearchSchema.parse({ page: 999_999 }).page).toBe(1)
  })

  it('rejects non-integer pagination values', () => {
    expect(personsSearchSchema.parse({ page: 2.5 }).page).toBe(1)
    expect(personsSearchSchema.parse({ pageSize: 10.5 }).pageSize).toBe(10)
  })

  it('keeps faceStatus a single string so the column filter can read it back', () => {
    // `useTableUrlState({ type: 'string' })` 按字符串读该键：归一化成数组会让
    // 筛选器永远显示"未选中"，而请求其实已经带上过滤。
    expect(
      personsSearchSchema.parse({ faceStatus: 'enrolled' }).faceStatus
    ).toBe('enrolled')
    expect(personsSearchSchema.parse({}).faceStatus).toBeUndefined()
  })

  it('collapses repeated faceStatus query keys to the first value', () => {
    expect(
      personsSearchSchema.parse({ faceStatus: ['enrolled', 'missing'] })
        .faceStatus
    ).toBe('enrolled')
  })

  it('drops unknown faceStatus values instead of forwarding them', () => {
    expect(
      personsSearchSchema.parse({ faceStatus: 'bogus' }).faceStatus
    ).toBeUndefined()
  })
})
