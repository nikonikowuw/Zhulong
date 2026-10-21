/**
 * Bounded, allowlisted browser error reporting.
 *
 * This logger exists to answer "what broke in the browser" on edge devices whose
 * console nobody can open. It deliberately does NOT upload what a normal
 * exception reporter uploads — no `Error.message`, no stack, no URL query, no
 * request body. Those are unbounded, attacker-influenced and frequently carry
 * credentials. Instead callers pass a stable event ID plus values from a closed
 * field set, and the server validates the same schema again.
 *
 * Limits that shape this file:
 *   - ring buffer 100, overflow drops oldest and counts
 *   - flush every 5s or at 20 pending entries, max 50 entries and 48 KiB per upload
 *   - dedup `(level,event)` to 3 per second
 *   - upload failure never calls log() and never retries immediately
 *
 * A crash loop is the failure mode this design must survive: it must not turn
 * one browser error into an unbounded request loop against an unauthenticated
 * endpoint on a small board.
 */

/** Only these levels are accepted. `debug` is dev-only and never uploaded. */
export type LogLevel = 'debug' | 'warn' | 'error'

/**
 * Stable event identifiers. Adding one requires a server-side allowlist review,
 * so this union is intentionally closed rather than `string`.
 */
export type LogEvent =
  | 'server.error'
  | 'query.retry'
  | 'query.error'
  | 'router.error_boundary'
  | 'window.error'
  | 'window.unhandledrejection'

/** Allowlisted field keys. Mirrors the server's allowlist. */
export interface LogFields {
  status?: number
  code?: string
  failureCount?: number
  errorName?: string
  queryKey?: string
  component?: string
  attempt?: number
  durationMs?: number
  httpStatus?: number
  retryCount?: number
  reasonClass?: string
  boundary?: string
  isRetryable?: boolean
}

/** A queued entry as sent to the server. */
interface QueueEntry {
  level: 'warn' | 'error'
  msg: LogEvent
  ts: string
  route?: string
  fields?: LogFields
}

const BUFFER_MAX = 100
const FLUSH_INTERVAL_MS = 5_000
const FLUSH_THRESHOLD = 20
const MAX_ENTRIES_PER_UPLOAD = 50
const MAX_ENCODED_BYTES = 48 * 1024
const DEDUP_LIMIT = 3
const DEDUP_WINDOW_MS = 1_000
const ENDPOINT = '/api/v1/client-logs'

/** Field values are bounded tokens, not arbitrary text. */
const MAX_FIELD_VALUE_LENGTH = 64
const ALLOWED_FIELD_KEYS = new Set<string>([
  'status',
  'code',
  'failureCount',
  'errorName',
  'queryKey',
  'component',
  'attempt',
  'durationMs',
  'httpStatus',
  'retryCount',
  'reasonClass',
  'boundary',
  'isRetryable',
])

const ALLOWED_EVENTS = new Set<string>([
  'server.error',
  'query.retry',
  'query.error',
  'router.error_boundary',
  'window.error',
  'window.unhandledrejection',
])

/** Route categories are allowlisted; dynamic path segments never appear. */
const ALLOWED_ROUTES = new Set<string>([
  'sign-in',
  'dashboard',
  'cameras.list',
  'cameras.detail',
  'tasks.list',
  'tasks.detail',
  'persons.list',
  'persons.detail',
  'algorithms.list',
  'records.list',
  'settings.network',
  'settings.storage',
  'settings.system',
  'audit.list',
  'unknown',
])

interface LoggerState {
  buffer: QueueEntry[]
  dropped: number
  dedup: Map<string, number[]>
  /** Single-flight gate shared by timer, threshold and hidden-page flushes. */
  inFlight: boolean
  timer: ReturnType<typeof setInterval> | null
  installed: boolean
  /**
   * Test-only override for production behaviour. The browser test runner always
   * reports DEV, so without this the upload path could never be exercised.
   */
  forceProduction: boolean | null
}

const state: LoggerState = {
  buffer: [],
  dropped: 0,
  dedup: new Map(),
  inFlight: false,
  timer: null,
  installed: false,
  forceProduction: null,
}

function isProduction(): boolean {
  if (state.forceProduction !== null) {
    return state.forceProduction
  }
  return import.meta.env.PROD === true
}

/** Bound a field value to a short token; anything else is discarded. */
function normalizeFieldValue(value: unknown): string | number | boolean | undefined {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : undefined
  }
  if (typeof value === 'boolean') {
    return value
  }
  if (typeof value === 'string') {
    // Strip control characters so a value cannot forge a log line, then bound.
    // Compared by code point rather than a regex: lint rejects a control-char
    // character class, and an explicit check states the intent more clearly.
    let cleaned = ''
    for (const character of value) {
      const codePoint = character.codePointAt(0) ?? 0
      if (codePoint < 0x20 || codePoint === 0x7f) {
        continue
      }
      cleaned += character
    }
    if (cleaned.length === 0) {
      return undefined
    }
    return cleaned.slice(0, MAX_FIELD_VALUE_LENGTH)
  }
  return undefined
}

function normalizeFields(fields: LogFields | undefined): LogFields | undefined {
  if (!fields) {
    return undefined
  }
  const normalized: LogFields = {}
  for (const [key, raw] of Object.entries(fields)) {
    if (!ALLOWED_FIELD_KEYS.has(key)) {
      continue
    }
    const value = normalizeFieldValue(raw)
    if (value === undefined) {
      continue
    }
    ;(normalized as Record<string, unknown>)[key] = value
  }
  return Object.keys(normalized).length > 0 ? normalized : undefined
}

function normalizeRoute(route: string | undefined): string | undefined {
  if (!route) {
    return undefined
  }
  return ALLOWED_ROUTES.has(route) ? route : 'unknown'
}

/** Dedup keys to DEDUP_LIMIT occurrences per sliding second. */
function isDuplicate(level: LogLevel, event: LogEvent, now: number): boolean {
  const key = `${level}:${event}`
  const timestamps = state.dedup.get(key) ?? []
  const recent = timestamps.filter((ts) => now - ts < DEDUP_WINDOW_MS)
  if (recent.length >= DEDUP_LIMIT) {
    state.dedup.set(key, recent)
    return true
  }
  recent.push(now)
  state.dedup.set(key, recent)
  return false
}

function recordDrop(count: number): void {
  // Cap locally at MAX_SAFE_INTEGER; the wire snapshot is clamped to 2^32-1.
  state.dropped = Math.min(state.dropped + count, Number.MAX_SAFE_INTEGER)
}

/**
 * Record a browser event. In development everything goes to the console and
 * nothing is uploaded; in production only warn/error are buffered.
 */
export function log(
  level: LogLevel,
  event: LogEvent,
  fields?: LogFields,
  route?: string
): void {
  if (!ALLOWED_EVENTS.has(event)) {
    return
  }

  if (!isProduction()) {
    // Development keeps the old visibility without touching the network.
    const payload = { ...normalizeFields(fields), route: normalizeRoute(route) }
    if (level === 'error') {
      // eslint-disable-next-line no-console
      console.error(`[${event}]`, payload)
    } else if (level === 'warn') {
      // eslint-disable-next-line no-console
      console.warn(`[${event}]`, payload)
    } else {
      // eslint-disable-next-line no-console
      console.log(`[${event}]`, payload)
    }
    return
  }

  if (level === 'debug') {
    return
  }

  if (isDuplicate(level, event, Date.now())) {
    return
  }

  const entry: QueueEntry = {
    level,
    msg: event,
    ts: new Date().toISOString(),
  }
  const normalizedRoute = normalizeRoute(route)
  if (normalizedRoute) {
    entry.route = normalizedRoute
  }
  const normalizedFields = normalizeFields(fields)
  if (normalizedFields) {
    entry.fields = normalizedFields
  }

  if (state.buffer.length >= BUFFER_MAX) {
    state.buffer.shift()
    recordDrop(1)
  }
  state.buffer.push(entry)

  if (state.buffer.length >= FLUSH_THRESHOLD) {
    void flush()
  }
}

/**
 * Pack buffered entries into batches respecting the entry and encoded-byte caps.
 *
 * A single entry larger than the whole byte budget is dropped rather than
 * blocking every later entry behind it, which would otherwise stall the queue
 * permanently once one oversized field got in.
 */
function buildBatches(): QueueEntry[][] {
  const batches: QueueEntry[][] = []
  let current: QueueEntry[] = []

  for (const entry of state.buffer) {
    const candidate = [...current, entry]
    const encodedLength = JSON.stringify({ entries: candidate }).length

    if (
      candidate.length > MAX_ENTRIES_PER_UPLOAD ||
      encodedLength > MAX_ENCODED_BYTES
    ) {
      if (current.length === 0) {
        // This single entry cannot ever fit; drop it and move on.
        recordDrop(1)
        continue
      }
      batches.push(current)
      current = [entry]
      // The entry may still be too large on its own; the next iteration's
      // candidate check handles that.
      if (JSON.stringify({ entries: current }).length > MAX_ENCODED_BYTES) {
        recordDrop(1)
        current = []
      }
      continue
    }
    current = candidate
  }
  if (current.length > 0) {
    batches.push(current)
  }
  return batches
}

async function postBatch(entries: QueueEntry[], dropped: number): Promise<boolean> {
  try {
    const response = await fetch(ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // The endpoint must work without a session, so credentials are omitted.
      credentials: 'omit',
      keepalive: true,
      body: JSON.stringify({ dropped, entries }),
    })
    return response.ok
  } catch {
    return false
  }
}

/**
 * Flush queued entries. Single-flight by design: a second call while an upload
 * is active returns immediately instead of stacking requests.
 *
 * Failure handling is deliberate — a failed upload counts its entries as
 * dropped and does NOT requeue or call log(). Requeueing would let a broken or
 * saturated endpoint create an unbounded retry loop from a crash-looping page.
 */
export async function flush(): Promise<void> {
  if (state.inFlight || state.buffer.length === 0) {
    return
  }
  state.inFlight = true
  try {
    const batches = buildBatches()
    let dropSnapshot = Math.min(state.dropped, 0xffffffff)

    for (let index = 0; index < batches.length; index += 1) {
      const batch = batches[index]
      // Send the drop snapshot once, with the first batch.
      const droppedForBatch = index === 0 ? dropSnapshot : 0
      const ok = await postBatch(batch, droppedForBatch)
      if (!ok) {
        // A failed batch is counted as dropped and removed. Keeping it would
        // let a broken or saturated endpoint build an unbounded retry backlog
        // from a crashing page, which is the loop this design must prevent.
        recordDrop(batch.length)
        state.buffer = state.buffer.filter((queued) => !batch.includes(queued))
        continue
      }
      // Remove only the entries that were accepted.
      state.buffer = state.buffer.filter((queued) => !batch.includes(queued))
      if (index === 0 && droppedForBatch > 0) {
        state.dropped = Math.max(0, state.dropped - droppedForBatch)
        dropSnapshot = 0
      }
    }
  } finally {
    state.inFlight = false
  }
}

/**
 * Flush on page hide. Uses sendBeacon for at most two batches.
 *
 * Beacon exposes no credentials-mode option, so this path cannot promise
 * `credentials: "omit"`; browsers may attach ambient same-origin cookies. The
 * endpoint therefore ignores session cookies for authorization and never logs
 * them. If a deployment requires strict omission, set `disableBeacon`.
 */
function flushOnHide(disableBeacon: boolean): void {
  if (state.inFlight || state.buffer.length === 0) {
    return
  }
  const batches = buildBatches()
  const limited = batches.slice(0, 2)
  let dropSnapshot = Math.min(state.dropped, 0xffffffff)

  for (let index = 0; index < limited.length; index += 1) {
    const batch = limited[index]
    const droppedForBatch = index === 0 ? dropSnapshot : 0
    const body = JSON.stringify({ dropped: droppedForBatch, entries: batch })

    if (disableBeacon || typeof navigator === 'undefined' || !navigator.sendBeacon) {
      // Fall back to a keepalive fetch; do not retry during teardown.
      void fetch(ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'omit',
        keepalive: true,
        body,
      }).catch(() => undefined)
      state.buffer = state.buffer.filter((queued) => !batch.includes(queued))
      continue
    }

    const accepted = navigator.sendBeacon(
      ENDPOINT,
      new Blob([body], { type: 'application/json' })
    )
    if (accepted) {
      state.buffer = state.buffer.filter((queued) => !batch.includes(queued))
      if (droppedForBatch > 0) {
        state.dropped = Math.max(0, state.dropped - droppedForBatch)
        dropSnapshot = 0
      }
    }
  }
}

/**
 * Install global capture once. Handlers report only an error *class*, never
 * `Error.message` or the stack.
 */
export function installGlobalErrorHandlers(disableBeacon = false): void {
  if (state.installed || typeof window === 'undefined') {
    return
  }
  state.installed = true

  window.addEventListener('error', () => {
    // Only the event class is reported; Error.message and the stack are
    // unbounded, attacker-influenced and regularly contain credentials.
    log('error', 'window.error')
  })

  window.addEventListener('unhandledrejection', () => {
    log('error', 'window.unhandledrejection')
  })

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      flushOnHide(disableBeacon)
    }
  })

  state.timer = setInterval(() => {
    void flush()
  }, FLUSH_INTERVAL_MS)
}

/** Test-only helpers. Not exported through any production path. */
export const __testing = {
  reset(): void {
    state.buffer = []
    state.dropped = 0
    state.dedup.clear()
    state.inFlight = false
    state.installed = false
    state.forceProduction = null
    if (state.timer !== null) {
      clearInterval(state.timer)
      state.timer = null
    }
  },
  bufferLength(): number {
    return state.buffer.length
  },
  droppedCount(): number {
    return state.dropped
  },
  pendingEntries(): QueueEntry[] {
    return [...state.buffer]
  },
  /**
   * Batch packing, exposed so tests can assert the caps deterministically.
   * Exercising it through flush() would race the threshold-triggered upload.
   */
  buildBatches(): QueueEntry[][] {
    return buildBatches()
  },
  /** Fill the buffer without triggering the threshold flush. */
  seed(entries: QueueEntry[]): void {
    state.buffer.push(...entries)
  },
  /** Force the production upload path on or off; null restores the real value. */
  setProduction(value: boolean | null): void {
    state.forceProduction = value
  },
  constants: {
    BUFFER_MAX,
    FLUSH_THRESHOLD,
    FLUSH_INTERVAL_MS,
    MAX_ENTRIES_PER_UPLOAD,
    MAX_ENCODED_BYTES,
    DEDUP_LIMIT,
  },
}
