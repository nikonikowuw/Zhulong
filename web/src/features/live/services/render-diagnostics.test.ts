import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderDiagnosticSession } from './render-diagnostics'

const frame = (timestamp: number) =>
  ({ timestamp, displayWidth: 640, displayHeight: 360 }) as VideoFrame

describe('createRenderDiagnosticSession', () => {
  let previousDiagnosticsFlag: boolean | undefined

  beforeEach(() => {
    previousDiagnosticsFlag = window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__
    window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__ = true
  })

  afterEach(() => {
    window.__ZHULONG_WEB_CODECS_DIAGNOSTICS__ = previousDiagnosticsFlag
    vi.restoreAllMocks()
  })

  it('reports source, rAF, and draw cadence independently', () => {
    const now = vi.spyOn(performance, 'now')
    now.mockReturnValueOnce(0).mockReturnValueOnce(10).mockReturnValueOnce(1010)
    const log = vi.fn()
    const session = createRenderDiagnosticSession(log)

    session.recordOutputFrame(frame(0), null)
    session.recordRafCallback(0)
    session.recordRafDelay(16)
    session.recordDraw(2, 0)

    session.recordOutputFrame(frame(10_000), null)
    session.recordRafCallback(16)
    session.recordRafDelay(18)
    session.recordDraw(4, 100)

    session.recordOutputFrame(frame(20_000), null)

    expect(log).toHaveBeenCalledWith(
      'info',
      '1s_render_summary',
      expect.objectContaining({
        outputFrames: 2,
        rafCallbacks: 2,
        drawCalls: 2,
        averageRafDelayMs: 17,
        averageRafIntervalMs: 16,
        averageOutputCallbackIntervalMs: 10,
        averageOutputFrameIntervalMs: 10,
        averageDrawIntervalMs: 100,
      })
    )
  })
})
