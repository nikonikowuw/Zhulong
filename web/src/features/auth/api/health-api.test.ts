import { describe, expect, it } from 'vitest'
import { mapHealthPayload, mapHealthResponse } from './health-api'

/**
 * 这里的样例直接抄自 running 的宿主响应（curl /api/v1/health），
 * 一旦后端改了字段名，`hardwareDecode` 会静默变 undefined —— 页脚就不再显示解码方式。
 */
describe('mapHealthPayload', () => {
  it('解析真实报文：snake_case 的 hardware_available', () => {
    const payload = {
      status: 'ready',
      components: { database: 'ready', engine: 'ready', logging: 'ready' },
      decode: {
        backend: 'auto',
        hardware_available: true,
        active_sessions: 0,
        max_sessions: 8,
        rejected_requests: 0,
      },
    }

    expect(mapHealthPayload(payload)).toEqual({
      state: 'ready',
      hardwareDecode: true,
    })
  })

  it('纯 CPU 机器上报 hardware_available=false 时不得声称硬件解码', () => {
    const payload = {
      status: 'ready',
      components: { database: 'ready', engine: 'ready' },
      decode: { backend: 'ffmpeg', hardware_available: false },
    }

    expect(mapHealthPayload(payload).hardwareDecode).toBe(false)
  })

  it('组件降级（如引擎无硬件且禁止软解）映射为 degraded', () => {
    const payload = {
      status: 'ready',
      components: { database: 'ready', engine: 'degraded' },
      decode: { backend: 'mpp', hardware_available: false },
    }

    expect(mapHealthPayload(payload).state).toBe('degraded')
  })

  it('老后端不带 decode 字段时解码方式保持未知', () => {
    const payload = {
      status: 'ready',
      components: { database: 'ready', engine: 'ready' },
    }

    expect(mapHealthPayload(payload).hardwareDecode).toBeUndefined()
  })

  it('未知状态 starting 或 error 不能被误报为 ready', () => {
    expect(mapHealthPayload({ status: 'starting' })).toEqual({
      state: 'starting',
    })
    expect(mapHealthPayload({ status: 'error' })).toEqual({ state: 'starting' })
  })

  it('组件载荷结构无效时回落到启动中', () => {
    expect(
      mapHealthPayload({ status: 'ready', components: ['ready'] })
    ).toEqual({ state: 'starting' })
  })

  it('校验完整 API 信封后才映射健康状态', () => {
    expect(
      mapHealthResponse({
        code: 'OK',
        message: 'OK',
        data: { status: 'ready', components: { engine: 'ready' } },
      })
    ).toEqual({ state: 'ready' })
    expect(
      mapHealthResponse({
        code: 'FAILED',
        message: 'unexpected response',
        data: { status: 'ready', components: { engine: 'ready' } },
      })
    ).toEqual({ state: 'starting' })
  })
})
