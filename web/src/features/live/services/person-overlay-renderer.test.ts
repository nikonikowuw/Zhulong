import { describe, expect, it, vi } from 'vitest'
import { createMockFace, createMockPerson } from '../types/ai-result'
import {
  drawPersonOverlay,
  isFaceCloseupPerson,
} from './person-overlay-renderer'

describe('person-overlay-renderer', () => {
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
      measureText: vi.fn().mockReturnValue({ width: 80 }),
      roundRect: vi.fn(),
    } as unknown as CanvasRenderingContext2D
  }

  it('clears the canvas even when there is nothing to draw', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 800,
      height: 600,
      persons: [],
      clear: true,
    })

    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 800, 600)
    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  it('does not clear the canvas when clear is false', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 800,
      height: 600,
      persons: [],
      clear: false,
    })

    expect(ctx.clearRect).not.toHaveBeenCalled()
  })

  it('draws a body box for a real person detection', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 1000,
      height: 1000,
      persons: [
        createMockPerson({
          track_id: 7,
          body_box: [0.1, 0.2, 0.5, 0.9],
          body_score: 0.87,
          faces: [createMockFace({ box: [0.2, 0.25, 0.3, 0.35] })],
        }),
      ],
    })

    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
    expect(ctx.fillText).toHaveBeenCalledWith(
      expect.stringContaining('87%'),
      expect.any(Number),
      expect.any(Number)
    )
  })

  it('draws a body box for a back-facing person without faces', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 1000,
      height: 1000,
      persons: [
        createMockPerson({
          body_box: [0.1, 0.2, 0.5, 0.9],
          body_score: 0.91,
          faces: [],
        }),
      ],
    })

    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
  })

  it('skips the degenerate face-closeup item so the face box is not doubled', () => {
    const ctx = createMockCtx()
    const faceBox: [number, number, number, number] = [0.2, 0.2, 0.4, 0.4]
    drawPersonOverlay({
      ctx,
      width: 1000,
      height: 1000,
      persons: [
        createMockPerson({
          track_id: 0,
          body_box: faceBox,
          body_score: 0.93,
          faces: [createMockFace({ box: faceBox })],
        }),
      ],
    })

    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  it('skips only the degenerate item in a mixed result', () => {
    const ctx = createMockCtx()
    const faceBox: [number, number, number, number] = [0.2, 0.2, 0.4, 0.4]
    drawPersonOverlay({
      ctx,
      width: 1000,
      height: 1000,
      persons: [
        createMockPerson({
          body_box: faceBox,
          faces: [createMockFace({ box: faceBox })],
        }),
        createMockPerson({
          body_box: [0.5, 0.1, 0.9, 0.95],
          faces: [],
        }),
      ],
    })

    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
  })

  it('clamps out-of-range coordinates to the canvas', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 800,
      height: 600,
      persons: [
        createMockPerson({
          body_box: [-0.2, -0.1, 1.4, 1.2],
          faces: [],
        }),
      ],
    })

    expect(ctx.strokeRect).toHaveBeenCalledWith(0, 0, 800, 600)
  })

  it('skips the label when the body box is too small to host it', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 1000,
      height: 1000,
      persons: [
        createMockPerson({ body_box: [0.1, 0.1, 0.11, 0.11], faces: [] }),
      ],
    })

    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
    expect(ctx.fillText).not.toHaveBeenCalled()
  })

  it('ignores boxes that collapse to zero area after clamping', () => {
    const ctx = createMockCtx()
    drawPersonOverlay({
      ctx,
      width: 800,
      height: 600,
      persons: [
        createMockPerson({ body_box: [0.3, 0.3, 0.3, 0.3], faces: [] }),
      ],
    })

    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  describe('isFaceCloseupPerson', () => {
    it('detects the aliased body box contract', () => {
      const faceBox: [number, number, number, number] = [0.15, 0.2, 0.35, 0.5]
      expect(
        isFaceCloseupPerson(
          createMockPerson({
            body_box: faceBox,
            faces: [createMockFace({ box: faceBox })],
          })
        )
      ).toBe(true)
    })

    it('does not treat a multi-face person as a closeup', () => {
      const faceBox: [number, number, number, number] = [0.15, 0.2, 0.35, 0.5]
      expect(
        isFaceCloseupPerson(
          createMockPerson({
            body_box: faceBox,
            faces: [createMockFace({ box: faceBox }), createMockFace()],
          })
        )
      ).toBe(false)
    })
  })
})
