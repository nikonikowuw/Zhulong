import { describe, expect, it } from 'vitest'
import { auditSearchSchema } from './schema'

describe('auditSearchSchema', () => {
  it('defaults out-of-range page sizes to the API default', () => {
    expect(auditSearchSchema.parse({ pageSize: 101 }).pageSize).toBe(20)
    expect(auditSearchSchema.parse({ pageSize: 100 }).pageSize).toBe(100)
  })

  it('preserves multiple action and status filters', () => {
    expect(
      auditSearchSchema.parse({
        action: ['camera.create', 'camera.delete'],
        status: ['success', 'failed'],
      })
    ).toMatchObject({
      action: ['camera.create', 'camera.delete'],
      status: ['success', 'failed'],
    })
  })

  it('drops impossible calendar dates from RFC3339 search filters', () => {
    expect(
      auditSearchSchema.parse({ startTime: '2026-02-30T12:00:00Z' })
    ).toMatchObject({ startTime: undefined })
  })

  it('drops invalid RFC3339 date filters', () => {
    expect(
      auditSearchSchema.parse({
        startTime: 'not-a-date',
        endTime: '2026-10-10T12:00:00Z',
      })
    ).toMatchObject({ startTime: undefined, endTime: '2026-10-10T12:00:00Z' })
  })
})
