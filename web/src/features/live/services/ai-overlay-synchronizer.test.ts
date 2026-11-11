import { describe, expect, it } from 'vitest'
import { parseAIResultEvent, type AIResultEventInput } from '../types/ai-result'
import { AIOverlaySynchronizer } from './ai-overlay-synchronizer'

function makeEvent(input: AIResultEventInput) {
  const parsed = parseAIResultEvent(input)
  if (!parsed) throw new Error('invalid mock AIResultEvent')
  return parsed
}

describe('AIOverlaySynchronizer', () => {
  it('isolates overlay state across multiple algorithms on the same camera', () => {
    const sync = new AIOverlaySynchronizer('main')

    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'face_rec',
        task_type: 'scene_structure',
        persons: [
          { track_id: 1, body_box: [0.1, 0.2, 0.4, 0.9], body_score: 0.9 },
        ],
        faces: [
          {
            box: [0.18, 0.22, 0.28, 0.34],
            person_index: 0,
            matched: true,
            person_name: '张三',
            similarity: 0.93,
          },
        ],
      }),
      100
    )

    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'plate_rec',
        task_type: 'vehicle_structure',
        vehicles: [
          {
            track_id: 9,
            vehicle_box: [0.5, 0.4, 0.85, 0.85],
            vehicle_score: 0.96,
            plates: [
              {
                box: [0.6, 0.7, 0.72, 0.76],
                score: 0.98,
                plate_number: '粤B12345',
                plate_color: 'blue',
                plate_type: 0,
              },
            ],
          },
        ],
      }),
      120
    )

    const both = sync.sample(130)
    expect(both.persons).toHaveLength(1)
    expect(both.faces).toHaveLength(1)
    expect(both.vehicles).toHaveLength(1)
    expect(both.hasActiveTargets).toBe(true)

    // 车牌算法发送空目标信标时，只清空车牌槽位，绝不误伤人脸与人体框
    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'plate_rec',
        task_type: 'vehicle_structure',
        vehicles: [],
      }),
      160
    )

    const afterPlateExit = sync.sample(165)
    expect(afterPlateExit.vehicles).toHaveLength(0)
    expect(afterPlateExit.persons).toHaveLength(1)
    expect(afterPlateExit.faces).toHaveLength(1)
  })

  it('buffers early AI events by pts_us on main stream until matching video frame is drawn', () => {
    const sync = new AIOverlaySynchronizer('main')

    // 当前画面正在渲染 pts_us = 100_000 (100ms)
    expect(sync.sample(100, 100_000).hasActiveTargets).toBe(false)

    // AI 推理较快（耗时 60ms < 视频抖动缓冲 160ms），pts_us = 200_000 的框提前到达
    const activatedImmediately = sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'helmet_det',
        task_type: 'detection',
        pts_us: 200_000,
        detections: [
          { box: [0.2, 0.2, 0.4, 0.5], label: 'helmet', score: 0.92 },
        ],
      }),
      110
    )
    expect(activatedImmediately).toBe(false)

    // 在 pts_us = 140_000 帧上仍未到呈现点，不应提前跳框
    expect(sync.sample(140, 140_000).detections).toHaveLength(0)

    // 当视频画到 pts_us = 200_000 帧时精准释放
    const aligned = sync.sample(200, 200_000)
    expect(aligned.detections).toHaveLength(1)
    expect(aligned.detections[0].label).toBe('helmet')
  })

  it('activates immediately on sub stream regardless of main-stream pts_us origin', () => {
    const sync = new AIOverlaySynchronizer('sub')

    // 子码流独立建立 PreviewNormalizer 起点（如 50_000），而主流 AI pts_us 为 900_000
    sync.sample(100, 50_000)
    const activated = sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'helmet_det',
        task_type: 'detection',
        pts_us: 900_000,
        detections: [
          { box: [0.1, 0.1, 0.3, 0.3], label: 'person', score: 0.9 },
        ],
      }),
      110
    )

    expect(activated).toBe(true)
    expect(sync.sample(115, 50_000).detections).toHaveLength(1)
  })

  it('smoothly extrapolates tracked persons and associated faces across intermediate video frames and snaps on teleport', () => {
    const sync = new AIOverlaySynchronizer('main')
    sync.sample(0, 0)

    // 第 1 次测量：pts_us = 0，人体在 x ∈ [0.10, 0.30]
    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'face_rec',
        task_type: 'scene_structure',
        pts_us: 0,
        persons: [
          { track_id: 5, body_box: [0.1, 0.2, 0.3, 0.8], body_score: 0.95 },
        ],
        faces: [
          { box: [0.15, 0.22, 0.25, 0.35], person_index: 0, matched: false },
        ],
      }),
      0
    )
    expect(sync.sample(0, 0).persons[0].body_box[0]).toBeCloseTo(0.1, 4)

    // 第 2 次测量：pts_us = 200_000 (200ms)，人体匀速右移 +0.08 至 x ∈ [0.18, 0.38]
    sync.sample(200, 200_000)
    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'face_rec',
        task_type: 'scene_structure',
        pts_us: 200_000,
        persons: [
          { track_id: 5, body_box: [0.18, 0.2, 0.38, 0.8], body_score: 0.95 },
        ],
        faces: [
          { box: [0.23, 0.22, 0.33, 0.35], person_index: 0, matched: false },
        ],
      }),
      200
    )

    // 激活瞬间（t=200ms）保持 C0 连续，不会发生阶跃跳变
    const at200 = sync.sample(200, 200_000)
    expect(at200.persons[0].body_box[0]).toBeCloseTo(0.1, 3)

    // 中间视频帧（t=300ms, pts_us=300_000）：残差已收敛且速度外推使人体框与关联人脸框平滑跟进至 > 0.18
    const at300 = sync.sample(300, 300_000)
    expect(at300.persons[0].body_box[0]).toBeGreaterThan(0.19)
    expect(at300.persons[0].body_box[0]).toBeLessThan(0.24)
    expect(at300.faces[0].box[0]).toBeGreaterThan(0.24)

    // 跨屏突变（> 0.22 中心跳变）：直接 Snap 瞬移，绝不拉出横跨全屏的幽灵滑行框
    sync.sample(400, 400_000)
    sync.ingest(
      makeEvent({
        camera_id: 'cam_01',
        algorithm_id: 'face_rec',
        task_type: 'scene_structure',
        pts_us: 400_000,
        persons: [
          { track_id: 5, body_box: [0.7, 0.2, 0.9, 0.8], body_score: 0.95 },
        ],
        faces: [],
      }),
      400
    )
    const snapped = sync.sample(400, 400_000)
    expect(snapped.persons[0].body_box[0]).toBeCloseTo(0.7, 4)
  })

  it('adapts fade TTL to observed cadence and expires stale slots cleanly', () => {
    const sync = new AIOverlaySynchronizer('main')

    // 模拟约 2fps（500ms 间隔）的低频推理算法，旧版固定 350ms 会每帧闪烁消失
    for (const t of [0, 500, 1000, 1500]) {
      sync.ingest(
        makeEvent({
          camera_id: 'cam_01',
          algorithm_id: 'slow_det',
          task_type: 'detection',
          detections: [
            { box: [0.1, 0.1, 0.3, 0.3], label: 'box', score: 0.88 },
          ],
        }),
        t
      )
    }

    // 距上次更新 420ms 时（> 旧版 350ms），自适应 TTL 仍保持框体稳定在线不闪烁
    expect(sync.sample(1920).detections).toHaveLength(1)

    // 超过自适应上限后安全消退清零
    expect(sync.sample(3100).detections).toHaveLength(0)
    expect(sync.sample(3100).hasActiveTargets).toBe(false)
  })
})
