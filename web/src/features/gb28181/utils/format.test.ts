import { describe, expect, it } from 'vitest'
import {
  EMPTY_PLACEHOLDER,
  deviceStateLevel,
  formatTimestamp,
  runtimeStateLevel,
} from './format'

describe('formatTimestamp', () => {
  it('formats an RFC 3339 timestamp instead of echoing it', () => {
    const iso = '2026-10-25T08:00:00Z'
    const formatted = formatTimestamp(iso, 'en-US')

    expect(formatted).not.toBe(iso)
    expect(formatted).not.toBe(EMPTY_PLACEHOLDER)
  })

  it('keeps an unparsable value verbatim rather than inventing a time', () => {
    expect(formatTimestamp('not-a-timestamp', 'en-US')).toBe('not-a-timestamp')
  })

  it('renders a placeholder for missing timestamps', () => {
    expect(formatTimestamp(null, 'en-US')).toBe(EMPTY_PLACEHOLDER)
    expect(formatTimestamp(undefined, 'en-US')).toBe(EMPTY_PLACEHOLDER)
    expect(formatTimestamp('', 'en-US')).toBe(EMPTY_PLACEHOLDER)
  })
})

describe('status levels', () => {
  it('treats a disabled subsystem as normal, not as a failure', () => {
    expect(runtimeStateLevel('disabled')).toBe('inactive')
    expect(runtimeStateLevel('running')).toBe('active')
    expect(runtimeStateLevel('error')).toBe('destructive')
  })

  it('falls back to inactive for states the UI does not know yet', () => {
    expect(runtimeStateLevel('somethig_new')).toBe('inactive')
    expect(deviceStateLevel('')).toBe('inactive')
  })

  it('maps device online state to the shared palette', () => {
    expect(deviceStateLevel('online')).toBe('active')
    expect(deviceStateLevel('offline')).toBe('destructive')
  })
})
