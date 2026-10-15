import { describe, it, expect, vi } from 'vitest'
import { createMockFace } from '../types/ai-result'
import { drawFaceOverlay } from './face-overlay-renderer'

describe('face-overlay-renderer', () => {
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
      arc: vi.fn(),
      fillText: vi.fn(),
      measureText: vi.fn().mockReturnValue({ width: 60 }),
      roundRect: vi.fn(),
    } as unknown as CanvasRenderingContext2D
  }

  it('clears canvas when faces array is empty', () => {
    const ctx = createMockCtx()
    drawFaceOverlay({
      ctx,
      width: 800,
      height: 600,
      faces: [],
    })

    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 800, 600)
    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  it('draws matched face with solid green box and name badge', () => {
    const ctx = createMockCtx()
    const face = createMockFace({
      box: [0.1, 0.2, 0.3, 0.4],
      quality: 0.9,
      matched: true,
      person_id: 'p_01',
      person_name: '张三',
      similarity: 0.98,
      landmarks: [
        [0.15, 0.25],
        [0.25, 0.25],
      ],
    })

    drawFaceOverlay({
      ctx,
      width: 1000,
      height: 1000,
      faces: [face],
    })

    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 1000, 1000)
    // 0.1 * 1000 = 100, 0.2 * 1000 = 200, bw = 200, bh = 200
    expect(ctx.strokeRect).toHaveBeenCalledWith(100, 200, 200, 200)
    expect(ctx.setLineDash).toHaveBeenCalledWith([])
    expect(ctx.fillText).toHaveBeenCalledWith(
      '张三 (98%)',
      expect.any(Number),
      expect.any(Number)
    )
    expect(ctx.arc).toHaveBeenCalledTimes(2)
  })

  it('draws unmatched face with dashed amber box and unknown label', () => {
    const ctx = createMockCtx()
    const face = createMockFace({
      box: [0.2, 0.2, 0.4, 0.5],
      quality: 0.7,
      matched: false,
      similarity: 0.42,
    })

    drawFaceOverlay({
      ctx,
      width: 1000,
      height: 1000,
      faces: [face],
      unrecognizedLabel: '未登记',
    })

    expect(ctx.strokeRect).toHaveBeenCalledWith(200, 200, 200, 300)
    expect(ctx.setLineDash).toHaveBeenCalledWith([4, 4])
    expect(ctx.fillText).toHaveBeenCalledWith(
      '未登记 (42%)',
      expect.any(Number),
      expect.any(Number)
    )
  })

  it('clamps coordinates safely when values exceed [0, 1]', () => {
    const ctx = createMockCtx()
    const face = createMockFace({
      box: [-0.1, -0.2, 1.5, 1.2],
      quality: 0.5,
      matched: true,
      person_id: 'p_02',
      person_name: '李四',
      similarity: 0.85,
    })

    drawFaceOverlay({
      ctx,
      width: 500,
      height: 500,
      faces: [face],
    })

    // Clamped box should be [0, 0, 500, 500]
    expect(ctx.strokeRect).toHaveBeenCalledWith(0, 0, 500, 500)
  })
})
