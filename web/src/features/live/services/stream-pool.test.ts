import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { StreamConnectionPool } from './stream-pool'

describe('StreamConnectionPool', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('单例模式正确返回同一实例', () => {
    const pool1 = StreamConnectionPool.getInstance()
    const pool2 = StreamConnectionPool.getInstance()
    expect(pool1).toBe(pool2)
  })

  it('多个订阅者订阅同一摄像机时复用连接并正确维护引用计数', () => {
    const pool = StreamConnectionPool.getInstance()
    const cameraId = 'test-cam-01'

    const sub1Status = vi.fn()
    const sub1Stats = vi.fn()
    const sub2Status = vi.fn()
    const sub2Stats = vi.fn()

    // 订阅 1
    const unsub1 = pool.subscribe(cameraId, 'main', {
      id: 'sub-1',
      onStatusChange: sub1Status,
      onStatsUpdate: sub1Stats,
    })

    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(1)
    expect(sub1Status).toHaveBeenCalledWith('idle', undefined)

    // 订阅 2 (复用)
    const unsub2 = pool.subscribe(cameraId, 'main', {
      id: 'sub-2',
      onStatusChange: sub2Status,
      onStatsUpdate: sub2Stats,
    })

    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(2)

    // 更新统计，两位订阅者均能收到广播
    pool.updateStats(cameraId, 'main', { fps: 30 })
    expect(sub1Stats).toHaveBeenCalledWith(expect.objectContaining({ fps: 30 }))
    expect(sub2Stats).toHaveBeenCalledWith(expect.objectContaining({ fps: 30 }))

    // 订阅 1 退出，计数减为 1，不销毁连接
    unsub1()
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(1)

    // 订阅 2 退出，计数减为 0
    unsub2()
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(0)

    // 快进 5 秒 (Grace Period 防抖)
    vi.advanceTimersByTime(5000)
    expect(pool.getStreamRefCount(cameraId, 'main')).toBe(0)
  })
})
