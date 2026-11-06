import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  escapeCsvField,
  formatLocalDateTime,
  toCsv,
  triggerDownload,
  UTF8_BOM,
} from './csv'

describe('csv', () => {
  describe('escapeCsvField', () => {
    it('escapes empty or null values', () => {
      expect(escapeCsvField(null)).toBe('')
      expect(escapeCsvField(undefined)).toBe('')
      expect(escapeCsvField('')).toBe('')
    })

    it('returns raw string when no special characters exist', () => {
      expect(escapeCsvField('hello_world')).toBe('hello_world')
      expect(escapeCsvField(12345)).toBe('12345')
    })

    it('wraps and escapes quotes, commas, and newlines', () => {
      expect(escapeCsvField('hello,world')).toBe('"hello,world"')
      expect(escapeCsvField('quote "test"')).toBe('"quote ""test"""')
      expect(escapeCsvField('multi\nline')).toBe('"multi\nline"')
      expect(escapeCsvField('multi\r\nline')).toBe('"multi\r\nline"')
    })

    it('stringifies objects', () => {
      expect(escapeCsvField({ a: 1, b: 'two' })).toBe(
        '"{""a"":1,""b"":""two""}"'
      )
    })

    it('neutralises spreadsheet formula injection', () => {
      expect(escapeCsvField('=1+1')).toBe("'=1+1")
      expect(escapeCsvField('@SUM(A1)')).toBe("'@SUM(A1)")
      expect(escapeCsvField('+8613800000000')).toBe("'+8613800000000")
      expect(escapeCsvField('-12:30')).toBe("'-12:30")
    })

    it('never prefixes plain numbers (would turn them into text)', () => {
      expect(escapeCsvField(-0.5)).toBe('-0.5')
      expect(escapeCsvField(-12345)).toBe('-12345')
    })
  })

  describe('toCsv', () => {
    it('emits BOM, header row and CRLF line breaks', () => {
      const csv = toCsv(
        ['id', 'name'],
        [
          [1, 'a,b'],
          [2, 'x"y'],
        ]
      )

      expect(csv).toBe(`${UTF8_BOM}id,name\r\n1,"a,b"\r\n2,"x""y"`)
    })

    it('keeps a header-only document valid', () => {
      expect(toCsv(['id'], [])).toBe(`${UTF8_BOM}id`)
    })
  })

  describe('formatLocalDateTime', () => {
    it('renders the local wall clock, not the UTC instant', () => {
      // 用本地时间构造再转 ISO，期望值不依赖运行环境时区
      const local = new Date(2026, 0, 2, 3, 4, 5)

      expect(formatLocalDateTime(local.toISOString())).toBe(
        '2026-01-02 03:04:05'
      )
    })

    it('falls back to the raw string when the timestamp is unparsable', () => {
      expect(formatLocalDateTime('not-a-timestamp')).toBe('not-a-timestamp')
      expect(formatLocalDateTime('')).toBe('')
    })
  })

  describe('triggerDownload', () => {
    afterEach(() => {
      vi.restoreAllMocks()
      vi.useRealTimers()
    })

    it('revokes the object URL only after the download has started', () => {
      vi.useFakeTimers()
      const createObjectURL = vi
        .spyOn(URL, 'createObjectURL')
        .mockReturnValue('blob:mock')
      const revokeObjectURL = vi
        .spyOn(URL, 'revokeObjectURL')
        .mockImplementation(() => {})
      const click = vi
        .spyOn(HTMLAnchorElement.prototype, 'click')
        .mockImplementation(() => {})

      triggerDownload(new Blob(['a,b'], { type: 'text/csv' }), 'audit.csv')

      expect(createObjectURL).toHaveBeenCalledOnce()
      expect(click).toHaveBeenCalledOnce()
      expect(click.mock.contexts[0]).toMatchObject({
        href: 'blob:mock',
        download: 'audit.csv',
      })
      // 立即 revoke 会让 Safari/Firefox 在下载真正启动前丢掉大 Blob
      expect(revokeObjectURL).not.toHaveBeenCalled()

      vi.advanceTimersByTime(10_000)
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock')
    })
  })
})
