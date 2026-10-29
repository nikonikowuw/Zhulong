import { describe, expect, it } from 'vitest'
import {
  PresentationScheduler,
  type PresentationSchedulerOptions,
} from './presentation-scheduler'

/**
 * 呈现调度器的契约：
 * 帧的呈现时刻由 PTS 决定，到达时刻的抖动不该改变它；
 * 只有真正错过呈现窗口的帧才允许被丢弃。
 */
function createScheduler(options?: PresentationSchedulerOptions) {
  return new PresentationScheduler<string>({
    targetLatencyMs: 100,
    maxLeadMs: 250,
    ...options,
  })
}

describe('PresentationScheduler', () => {
  it('首帧按目标延迟排期，到点前不呈现', () => {
    const scheduler = createScheduler()

    const pushed = scheduler.push(0, 'a', 0)
    expect(pushed).toEqual({ dropped: [], rebased: false })

    expect(scheduler.takeDue(0).present).toBeNull()
    expect(scheduler.takeDue(99).present).toBeNull()
    const due = scheduler.takeDue(100)
    expect(due.present).toBe('a')
    expect(due.expired).toEqual([])
    expect(due.pending).toBe(0)
  })

  it('到达抖动不改变呈现时刻（抖动缓冲的意义所在）', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    // 第二帧准点到，第三帧晚到 20ms：呈现时刻仍只由 PTS 决定
    scheduler.push(40_000, 'b', 40)
    scheduler.push(80_000, 'c', 100)

    expect(scheduler.takeDue(100).present).toBe('a')
    expect(scheduler.takeDue(139).present).toBeNull()
    expect(scheduler.takeDue(140).present).toBe('b')
    expect(scheduler.takeDue(160).present).toBeNull()
    expect(scheduler.takeDue(180).present).toBe('c')
  })

  it('同一拍内多帧到期时只呈现最新一帧，其余记为过期', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    // 两帧挤进同一个刷新区间：先到的那帧不能挤掉后到的那帧（旧实现的病根）
    scheduler.push(40_000, 'b', 1)

    const presentation = scheduler.takeDue(150)
    expect(presentation.present).toBe('b')
    expect(presentation.expired).toEqual(['a'])
    expect(presentation.pending).toBe(0)
  })

  it('小幅 PTS 回退（B 帧重排）沿用已排期时刻，不重建时钟', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    scheduler.push(120_000, 'b', 10)
    const reordered = scheduler.push(80_000, 'c', 20)

    expect(reordered.rebased).toBe(false)
    expect(reordered.dropped).toEqual([])

    const presentation = scheduler.takeDue(230)
    expect(presentation.present).toBe('c')
    expect(presentation.expired).toEqual(['a', 'b'])
    expect(presentation.rebased).toBe(false)
  })

  it('时间轴整段回退（相机重启）丢弃旧队列并重建时钟', () => {
    const scheduler = createScheduler()
    scheduler.push(2_000_000, 'a', 0)
    scheduler.push(2_040_000, 'b', 10)

    const afterReboot = scheduler.push(100, 'c', 20)
    expect(afterReboot.rebased).toBe(true)
    expect(afterReboot.dropped).toEqual(['a', 'b'])
    expect(scheduler.pending).toBe(1)
  })

  it('源流时间跳到墙钟前面时不静默冻结', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    // 服务端整段丢帧后等下一个 IDR：下一帧的 PTS 直接跳了 2 秒
    const jumped = scheduler.push(2_000_000, 'b', 50)

    // 陈旧帧在入队时就被时间深度裁掉，不会让画面干等 2 秒
    expect(jumped.dropped).toEqual(['a'])

    const rebased = scheduler.takeDue(100)
    expect(rebased.rebased).toBe(true)
    expect(rebased.present).toBeNull()

    // 重锚到新的时间起点后，只等一个目标延迟就出画
    expect(scheduler.takeDue(199).present).toBeNull()
    expect(scheduler.takeDue(200).present).toBe('b')
  })

  it('队列跑空后重新建立映射，不累积漂移', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    expect(scheduler.takeDue(100).present).toBe('a')

    // 队列空过一次：下一帧重新获得完整的目标延迟，而不是「立刻到期」退化成无缓冲
    scheduler.push(40_000, 'b', 140)
    expect(scheduler.takeDue(200).present).toBeNull()
    expect(scheduler.takeDue(240).present).toBe('b')
  })

  it('超过队列上限时丢弃最旧的帧', () => {
    const scheduler = createScheduler({ maxQueueFrames: 3 })
    expect(scheduler.push(0, 'a', 0).dropped).toEqual([])
    scheduler.push(40_000, 'b', 10)
    scheduler.push(80_000, 'c', 20)
    const overflow = scheduler.push(120_000, 'd', 30)

    expect(overflow.dropped).toEqual(['a'])
    expect(scheduler.pending).toBe(3)
  })

  it('起播补发整个 GOP 时按时间深度裁顶，不留下恒定落后', () => {
    const scheduler = createScheduler({ maxDepthMs: 250 })

    // 服务端补发 50 帧（2 秒 GOP）一次性涌入：若只按帧数裁顶，
    // 队列会永久保持满窗陈旧帧，画面恒定落后实时一整个队列的时间。
    const dropped: string[] = []
    for (let index = 0; index < 50; index++) {
      const label = `f${index}`
      dropped.push(...scheduler.push(index * 40_000, label, 0).dropped)
    }

    // 只保留 250ms 以内的 7 帧，其余 43 帧直接丢弃
    expect(dropped).toHaveLength(43)
    expect(scheduler.pending).toBe(7)

    const rebased = scheduler.takeDue(0)
    expect(rebased.rebased).toBe(true)
    expect(rebased.present).toBeNull()

    // 重锚后从保留窗口的头部开始出画，延迟回到目标量级
    expect(scheduler.takeDue(100).present).toBe('f43')
    expect(scheduler.takeDue(140).present).toBe('f44')
  })

  it('clear 交还全部待呈现帧并作废时钟', () => {
    const scheduler = createScheduler()
    scheduler.push(0, 'a', 0)
    scheduler.push(40_000, 'b', 10)

    expect(scheduler.clear()).toEqual(['a', 'b'])
    expect(scheduler.pending).toBe(0)

    // 时钟已作废：下一帧重新从完整目标延迟开始排期
    scheduler.push(80_000, 'c', 500)
    expect(scheduler.takeDue(599).present).toBeNull()
    expect(scheduler.takeDue(600).present).toBe('c')
  })
})
