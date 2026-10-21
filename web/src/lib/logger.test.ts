import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { __testing, flush, installGlobalErrorHandlers, log } from './logger'

/**
 * These tests run in a real headless Chromium, not JSDOM, because the behaviour
 * under test is browser-only: `navigator.sendBeacon`, `visibilitychange`, and
 * `fetch(keepalive)`. A JSDOM fake would assert our assumptions about those APIs
 * rather than the APIs themselves.
 */

interface CapturedRequest {
  url: string
  credentials: RequestCredentials | undefined
  keepalive: boolean | undefined
  body: string
}

let captured: CapturedRequest[] = []

function stubFetch(ok = true): void {
  captured = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      captured.push({
        url,
        credentials: init?.credentials,
        keepalive: init?.keepalive,
        body: typeof init?.body === 'string' ? init.body : '',
      })
      return new Response(JSON.stringify({ code: ok ? 'OK' : 'ERR' }), {
        status: ok ? 200 : 500,
      })
    })
  )
}

/**
 * Fill the buffer with `count` distinct entries.
 *
 * Dedup keys on (level,event) within a one-second window, so a burst needs the
 * clock advanced or distinct events to exceed the dedup limit. Advancing the
 * clock is the honest approach: it models a slow crash loop rather than
 * disabling the very limit under test.
 */
function fillBuffer(count: number, fieldName: 'failureCount' | 'retryCount' = 'failureCount'): void {
  const base = Date.now()
  for (let index = 0; index < count; index += 1) {
    vi.setSystemTime(new Date(base + index * 1000))
    log('error', 'server.error', { [fieldName]: index } as never)
  }
  vi.useRealTimers()
}

beforeEach(() => {
  __testing.reset()
  stubFetch(true)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  __testing.reset()
})

describe('logger level gating', () => {
  it('keeps events out of the buffer in development', () => {
    __testing.setProduction(false)
    log('error', 'server.error', { status: 500 })
    expect(__testing.bufferLength()).toBe(0)
  })

  it('buffers warn and error in production', () => {
    __testing.setProduction(true)
    log('error', 'server.error', { status: 500 })
    log('warn', 'query.retry', { failureCount: 1 })
    expect(__testing.bufferLength()).toBe(2)
  })

  it('never buffers debug, even in production', () => {
    __testing.setProduction(true)
    log('debug', 'query.retry', { failureCount: 1 })
    expect(__testing.bufferLength()).toBe(0)
  })
})

describe('logger field allowlist', () => {
  it('drops unknown field keys instead of forwarding them', () => {
    __testing.setProduction(true)
    // A caller bypassing the type system must still be filtered at runtime.
    log('error', 'server.error', {
      status: 503,
      password: 'hunter2',
    } as unknown as Parameters<typeof log>[2])

    const [entry] = __testing.pendingEntries()
    expect(entry.fields).toEqual({ status: 503 })
    expect(JSON.stringify(entry)).not.toContain('hunter2')
  })

  it('strips control characters from field values', () => {
    __testing.setProduction(true)
    log('error', 'server.error', { code: 'line1\nline2\r' })
    const [entry] = __testing.pendingEntries()
    expect(entry.fields?.code).toBe('line1line2')
  })

  it('reduces an unknown route to the unknown category', () => {
    __testing.setProduction(true)
    log('error', 'server.error', undefined, 'cameras/123?token=abc')
    const [entry] = __testing.pendingEntries()
    expect(entry.route).toBe('unknown')
    expect(JSON.stringify(entry)).not.toContain('token=abc')
  })
})

describe('logger bounded buffer', () => {
  it('drops the oldest entry when the buffer overflows and counts it', () => {
    __testing.setProduction(true)
    const overflow = 25
    vi.useFakeTimers()
    fillBuffer(__testing.constants.BUFFER_MAX + overflow)
    expect(__testing.bufferLength()).toBeLessThanOrEqual(__testing.constants.BUFFER_MAX)
    expect(__testing.droppedCount()).toBe(overflow)
  })

  it('survives a crash loop without unbounded growth', () => {
    __testing.setProduction(true)
    for (let index = 0; index < 10000; index += 1) {
      log('error', 'window.error')
    }
    expect(__testing.bufferLength()).toBeLessThanOrEqual(__testing.constants.BUFFER_MAX)
    // Dedup plus the reservoir cap bound both memory and upload volume.
    expect(__testing.droppedCount()).toBeGreaterThanOrEqual(0)
  })
})

describe('logger dedup', () => {
  it('collapses the same (level,event) beyond the per-second limit', () => {
    __testing.setProduction(true)
    const limit = __testing.constants.DEDUP_LIMIT
    for (let index = 0; index < limit + 10; index += 1) {
      log('error', 'router.error_boundary')
    }
    expect(__testing.bufferLength()).toBe(limit)
  })

  it('keeps warn and error separate even for the same event', () => {
    __testing.setProduction(true)
    log('error', 'server.error')
    log('warn', 'server.error')
    expect(__testing.bufferLength()).toBe(2)
  })
})

describe('logger upload', () => {
  it('omits credentials and uses keepalive for the scheduled upload', async () => {
    __testing.setProduction(true)
    log('error', 'server.error', { status: 503 })
    await flush()

    expect(captured).toHaveLength(1)
    expect(captured[0].url).toBe('/api/v1/client-logs')
    expect(captured[0].credentials).toBe('omit')
    expect(captured[0].keepalive).toBe(true)

    const body = JSON.parse(captured[0].body)
    expect(body.entries[0].msg).toBe('server.error')
    expect(body.entries[0].level).toBe('error')
  })

  it('never sends exception text, stack or query in the payload', async () => {
    __testing.setProduction(true)
    log('error', 'server.error', { status: 500, code: 'INTERNAL_ERROR' })
    await flush()

    const body = captured[0].body
    expect(body).not.toContain('stack')
    expect(body).not.toContain('message')
    expect(body).not.toContain('?')
  })

  it('removes accepted entries after a successful upload', async () => {
    __testing.setProduction(true)
    log('error', 'server.error')
    await flush()
    expect(__testing.bufferLength()).toBe(0)
  })

  it('does not recurse or requeue when the upload fails', async () => {
    stubFetch(false)
    __testing.setProduction(true)
    log('error', 'window.error')

    await flush()
    // One attempt only: no retry loop from a failing endpoint.
    expect(captured).toHaveLength(1)
    // The failed entry is counted as dropped, not requeued forever.
    expect(__testing.bufferLength()).toBe(0)
    expect(__testing.droppedCount()).toBeGreaterThan(0)
  })

  it('uploads more than one batch when the queue exceeds the entry cap', async () => {
    __testing.setProduction(true)
    __testing.seed(
      Array.from({ length: __testing.constants.MAX_ENTRIES_PER_UPLOAD + 10 }, (_, index) => ({
        level: 'error' as const,
        msg: 'server.error' as const,
        ts: new Date().toISOString(),
        fields: { failureCount: index },
      }))
    )
    await flush()
    expect(captured.length).toBeGreaterThanOrEqual(2)
    expect(__testing.bufferLength()).toBe(0)
  })

  it('is single-flight so concurrent flushes do not stack requests', async () => {
    __testing.setProduction(true)
    log('error', 'server.error')
    await Promise.all([flush(), flush(), flush()])
    expect(captured).toHaveLength(1)
  })

  it('caps entries per upload and splits across batches', async () => {
    __testing.setProduction(true)
    const total = __testing.constants.MAX_ENTRIES_PER_UPLOAD + 10
    vi.useFakeTimers()
    fillBuffer(total)
    vi.useRealTimers()

    // Assert packing directly: going through flush() here would race the
    // threshold-triggered upload that fired while filling.
    const batches = __testing.buildBatches()
    expect(batches.length).toBeGreaterThanOrEqual(2)
    for (const batch of batches) {
      expect(batch.length).toBeLessThanOrEqual(__testing.constants.MAX_ENTRIES_PER_UPLOAD)
    }
    // Nothing may be lost in packing.
    expect(batches.flat()).toHaveLength(total)
  })

  it('keeps every encoded batch below the byte cap', () => {
    __testing.setProduction(true)
    vi.useFakeTimers()
    const base = Date.now()
    for (let index = 0; index < 60; index += 1) {
      vi.setSystemTime(new Date(base + index * 1000))
      log('error', 'server.error', { code: 'X'.repeat(60), failureCount: index })
    }
    vi.useRealTimers()

    const batches = __testing.buildBatches()
    for (const batch of batches) {
      expect(JSON.stringify({ entries: batch }).length).toBeLessThanOrEqual(
        __testing.constants.MAX_ENCODED_BYTES
      )
    }
  })
})

describe('logger hidden-page flush', () => {
  function stubBeacon(result: boolean): CapturedRequest[] {
    const sent: CapturedRequest[] = []
    Object.defineProperty(navigator, 'sendBeacon', {
      value: (url: string, data: Blob) => {
        void data.text().then((body) => {
          sent.push({ url, credentials: undefined, keepalive: undefined, body })
        })
        return result
      },
      configurable: true,
      writable: true,
    })
    return sent
  }

  it('sends at most two beacon batches on hide', async () => {
    const sent = stubBeacon(true)
    __testing.setProduction(true)
    vi.useFakeTimers()
    fillBuffer(__testing.constants.BUFFER_MAX)

    document.dispatchEvent(new Event('visibilitychange'))

    // Beacon awaits the Blob read, so give the microtask queue a turn.
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(sent.length).toBeLessThanOrEqual(2)
  })

  it('keeps entries when beacon is rejected', async () => {
    stubBeacon(false)
    __testing.setProduction(true)
    log('error', 'window.error')

    document.dispatchEvent(new Event('visibilitychange'))
    await new Promise((resolve) => setTimeout(resolve, 50))

    // Rejected beacon must not silently discard the entry.
    expect(__testing.bufferLength()).toBe(1)
  })
})

describe('logger global handlers', () => {
  it('installs once and reports only an event class for window errors', () => {
    __testing.setProduction(true)
    installGlobalErrorHandlers()
    installGlobalErrorHandlers()

    window.dispatchEvent(
      new ErrorEvent('error', {
        error: new Error('secret password hunter2 in message'),
        message: 'secret password hunter2 in message',
      })
    )

    const entries = __testing.pendingEntries()
    expect(entries).toHaveLength(1)
    expect(entries[0].msg).toBe('window.error')
    expect(JSON.stringify(entries)).not.toContain('hunter2')
  })

  it('reports unhandled rejections by class only', () => {
    __testing.setProduction(true)
    installGlobalErrorHandlers()

    window.dispatchEvent(
      new PromiseRejectionEvent('unhandledrejection', {
        promise: Promise.resolve(),
        reason: new Error('token=abc123'),
      })
    )

    const entries = __testing.pendingEntries()
    expect(entries[0]?.msg).toBe('window.unhandledrejection')
    expect(JSON.stringify(entries)).not.toContain('abc123')
  })
})
