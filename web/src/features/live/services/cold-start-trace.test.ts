import { describe, expect, it, vi } from 'vitest'
import { createPoolColdStartTrace, stageDeltas } from './cold-start-trace'

interface Report {
  event: string
  details: Record<string, unknown>
}

function packet(seq: number, isKeyFrame: boolean) {
  return { seq, isKeyFrame }
}

describe('stageDeltas', () => {
  it('以首包为原点换算各段毫秒偏移', () => {
    expect(
      stageDeltas({
        originAtMs: 100,
        stages: { submit: 130, output: 180, drawn: 260 },
      })
    ).toEqual({ submit: 30, output: 80, drawn: 160 })
  })

  it('把缺失的观测点保留为 null，绝不当作 0ms', () => {
    expect(
      stageDeltas({ originAtMs: 100, stages: { submit: null, drawn: 260 } })
    ).toEqual({ submit: null, drawn: 160 })
  })

  it('原点缺失时全部返回 null', () => {
    expect(stageDeltas({ originAtMs: null, stages: { drawn: 260 } })).toEqual({
      drawn: null,
    })
  })
})

describe('createPoolColdStartTrace', () => {
  it('等到首个关键帧后交卷，并统计之前的非关键帧数', () => {
    let clock = 1000
    const reports: Report[] = []
    const trace = createPoolColdStartTrace({
      cameraId: 'cam-1',
      streamType: 'main',
      now: () => clock,
      report: (event, details) => reports.push({ event, details }),
    })

    trace.observeStatus('connecting')
    trace.observeStatus('connected')
    clock = 1120
    trace.observePacket(packet(10, false))
    trace.observePacket(packet(11, false))
    clock = 1200
    trace.observePacket(packet(12, true))

    expect(reports).toHaveLength(1)
    expect(reports[0].event).toBe('cold_start_stream')
    expect(reports[0].details).toMatchObject({
      camera_id: 'cam-1',
      stream_type: 'main',
      outcome: 'keyframe',
      first_packet_ms: 120,
      first_keyframe_packet_ms: 200,
      packets_before_keyframe: 2,
      packets_total: 3,
      first_packet_seq: 10,
    })
  })

  it('迟迟没有关键帧时超时交卷，把「在等 IDR」变成可见数据', () => {
    vi.useFakeTimers()
    try {
      const reports: Report[] = []
      const trace = createPoolColdStartTrace({
        cameraId: 'cam-2',
        streamType: 'sub',
        now: () => 0,
        report: (event, details) => reports.push({ event, details }),
      })

      trace.observeStatus('connected')
      trace.observePacket(packet(1, false))
      trace.observePacket(packet(2, false))
      expect(reports).toHaveLength(0)

      vi.advanceTimersByTime(5000)

      expect(reports).toHaveLength(1)
      expect(reports[0].details).toMatchObject({
        outcome: 'timeout',
        packets_before_keyframe: 2,
        first_keyframe_packet_ms: null,
      })
    } finally {
      vi.useRealTimers()
    }
  })

  it('同一个订阅只上报一次，dispose 不重复交卷', () => {
    const reports: Report[] = []
    const trace = createPoolColdStartTrace({
      cameraId: 'cam-3',
      streamType: 'main',
      now: () => 0,
      report: (event, details) => reports.push({ event, details }),
    })

    trace.observeStatus('connected')
    trace.observePacket(packet(1, true))
    trace.observePacket(packet(2, true))
    trace.dispose()

    expect(reports).toHaveLength(1)
    expect(reports[0].details.outcome).toBe('keyframe')
  })
})
