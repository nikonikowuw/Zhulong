import { describe, expect, it, vi } from 'vitest'
import { drawDetectionOverlay } from './detection-overlay-renderer'

describe('detection-overlay-renderer', () => {
  function createMockCtx(): CanvasRenderingContext2D {
    return {
      clearRect: vi.fn(),
      save: vi.fn(),
      restore: vi.fn(),
      strokeRect: vi.fn(),
      fillRect: vi.fn(),
      setLineDash: vi.fn(),
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      stroke: vi.fn(),
      fill: vi.fn(),
      fillText: vi.fn(),
      measureText: vi.fn().mockReturnValue({ width: 72 }),
      roundRect: vi.fn(),
    } as unknown as CanvasRenderingContext2D
  }

  it('clears canvas when clear is true and detections is empty', () => {
    const ctx = createMockCtx()
    drawDetectionOverlay({
      ctx,
      width: 640,
      height: 360,
      detections: [],
      clear: true,
    })
    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 640, 360)
    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  it('draws clamped detection box and label with score percentage', () => {
    const ctx = createMockCtx()
    drawDetectionOverlay({
      ctx,
      width: 1000,
      height: 500,
      detections: [
        {
          box: [-0.1, 0.2, 0.4, 1.2],
          label: 'helmet',
          score: 0.93,
        },
      ],
      clear: false,
    })

    expect(ctx.clearRect).not.toHaveBeenCalled()
    expect(ctx.strokeRect).toHaveBeenCalledWith(0, 100, 400, 400)
    expect(ctx.fillText).toHaveBeenCalledWith(
      'helmet (93%)',
      expect.any(Number),
      expect.any(Number)
    )
  })

  it('skips zero-area boxes and falls back to fallbackLabel when label is empty', () => {
    const ctx = createMockCtx()
    drawDetectionOverlay({
      ctx,
      width: 800,
      height: 600,
      detections: [
        { box: [0.3, 0.3, 0.3, 0.5], label: 'ignored', score: 0.9 },
        { box: [0.1, 0.1, 0.4, 0.5], label: '   ', score: 0 },
      ],
      fallbackLabel: '异常物',
    })

    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
    expect(ctx.fillText).toHaveBeenCalledWith(
      '异常物',
      expect.any(Number),
      expect.any(Number)
    )
  })
})

