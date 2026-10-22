import { describe, it, expect, vi } from 'vitest'
import { createMockVehicle } from '../types/ai-result'
import { drawVehicleOverlay } from './vehicle-overlay-renderer'

describe('vehicle-overlay-renderer', () => {
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

  it('clears canvas when vehicles array is empty and clear is true', () => {
    const ctx = createMockCtx()
    drawVehicleOverlay({
      ctx,
      width: 800,
      height: 600,
      vehicles: [],
      clear: true,
    })

    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 800, 600)
    expect(ctx.strokeRect).not.toHaveBeenCalled()
  })

  it('does not clear canvas when clear is false', () => {
    const ctx = createMockCtx()
    drawVehicleOverlay({
      ctx,
      width: 800,
      height: 600,
      vehicles: [],
      clear: false,
    })

    expect(ctx.clearRect).not.toHaveBeenCalled()
  })

  it('draws degraded vehicle plate with plate box and text badge', () => {
    const ctx = createMockCtx()
    const vehicle = createMockVehicle({
      track_id: 0,
      vehicle_box: [0.2, 0.3, 0.4, 0.5],
      vehicle_score: 0.95,
      plates: [
        {
          box: [0.2, 0.3, 0.4, 0.5],
          score: 0.96,
          plate_number: '粤B88888',
          plate_color: '蓝色',
          plate_type: 0,
        },
      ],
    })

    drawVehicleOverlay({
      ctx,
      width: 1000,
      height: 1000,
      vehicles: [vehicle],
      clear: true,
    })

    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 1000, 1000)
    // 退化模式下只绘制车牌框，不重复绘制外层车身框
    expect(ctx.strokeRect).toHaveBeenCalledTimes(1)
    expect(ctx.strokeRect).toHaveBeenCalledWith(200, 300, 200, 200)
    expect(ctx.fillText).toHaveBeenCalledWith(
      '粤B88888 (96%)',
      expect.any(Number),
      expect.any(Number)
    )
  })

  it('draws both vehicle body and plate box in structured mode', () => {
    const ctx = createMockCtx()
    const vehicle = createMockVehicle({
      track_id: 101,
      vehicle_box: [0.1, 0.1, 0.8, 0.8],
      vehicle_score: 0.92,
      vehicle_type: 'SUV',
      vehicle_color: '白色',
      plates: [
        {
          box: [0.4, 0.6, 0.5, 0.65],
          score: 0.98,
          plate_number: '京A12345',
          plate_color: '黄色',
          plate_type: 1,
        },
      ],
    })

    drawVehicleOverlay({
      ctx,
      width: 1000,
      height: 1000,
      vehicles: [vehicle],
      clear: true,
    })

    // 绘制车身框 + 车牌框
    expect(ctx.strokeRect).toHaveBeenCalledTimes(2)
    // 车身框: 100, 100, 700, 700
    expect(ctx.strokeRect).toHaveBeenCalledWith(100, 100, 700, 700)
    // 车牌框: 400, 600, 100, 50
    expect(ctx.strokeRect).toHaveBeenCalledWith(400, 600, 100, 50)

    // 车身标签和车牌标签
    expect(ctx.fillText).toHaveBeenCalledWith(
      'SUV · 白色 · #101',
      expect.any(Number),
      expect.any(Number)
    )
    expect(ctx.fillText).toHaveBeenCalledWith(
      '京A12345 (98%)',
      expect.any(Number),
      expect.any(Number)
    )
  })

  it('handles unrecognized plate label with custom text', () => {
    const ctx = createMockCtx()
    const vehicle = createMockVehicle({
      track_id: 0,
      vehicle_box: [0.2, 0.3, 0.4, 0.5],
      vehicle_score: 0.8,
      plates: [
        {
          box: [0.2, 0.3, 0.4, 0.5],
          score: 0.82,
          plate_number: '',
          plate_color: '',
          plate_type: 0,
        },
      ],
    })

    drawVehicleOverlay({
      ctx,
      width: 1000,
      height: 1000,
      vehicles: [vehicle],
      unrecognizedLabel: '自定义未识别',
      clear: false,
    })

    expect(ctx.clearRect).not.toHaveBeenCalled()
    expect(ctx.fillText).toHaveBeenCalledWith(
      '自定义未识别 (82%)',
      expect.any(Number),
      expect.any(Number)
    )
  })
})
