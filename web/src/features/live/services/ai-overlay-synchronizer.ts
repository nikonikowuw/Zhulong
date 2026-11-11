import { type StreamType } from '../types'
import {
  extractFacesFromEvent,
  type AIResultEvent,
  type DetectionItem,
  type FaceStructureItem,
  type PersonItem,
  type PlateSubItem,
  type VehicleItem,
} from '../types/ai-result'

export type BoxTuple = [number, number, number, number]

/** 允许早到事件直接激活的半帧容差（微秒，20ms ≈ 25fps 半帧）。 */
const PTS_EARLY_TOLERANCE_US = 20_000

/** 主流 PTS 与 AI 事件 pts_us 允许的最大时钟偏差（2 秒），超出则降级墙钟立即激活。 */
const MAX_PTS_SKEW_US = 2_000_000

/** 视频暂停或卡顿时，待释放队列的最长滞留保护时间（毫秒）。 */
const MAX_PENDING_HOLD_MS = 260

/** 单算法槽位最多缓存的早到事件数。 */
const MAX_PENDING_PER_SLOT = 8

/** 残差平滑收敛时长（毫秒，约 2 帧 @25fps）。 */
const BLEND_DURATION_MS = 85

/** 基于 track_id 的最长外推窗口（毫秒）与单边最大归一化位移上限。 */
const MAX_EXTRAPOLATION_MS = 200
const MAX_EXTRAP_SHIFT = 0.08
const VELOCITY_DAMPING = 0.78

/** 判定同一 track_id 连续运动（非跨屏跳变/ID 漂移）的中心点距离与面积比阈值。 */
const MAX_SMOOTH_CENTER_JUMP = 0.22
const MIN_SMOOTH_AREA_RATIO = 0.28
const MAX_SMOOTH_AREA_RATIO = 3.5

/** 自适应消退 TTL 参数（毫秒）。 */
const DEFAULT_INTERVAL_MS = 200
const MIN_TTL_MS = 450
const MAX_TTL_MS = 1500
const TTL_MULTIPLIER = 2.4

interface TrackMotionState {
  rawBox: BoxTuple
  rawAtMs: number
  rawPtsUs?: number
  velPerMs: BoxTuple
  hasVelocity: boolean
  blendOffset: BoxTuple
}

interface PendingAIEvent {
  event: AIResultEvent
  enqueuedAtMs: number
}

interface ActiveSlotPayload {
  activatedAtMs: number
  ptsUs?: number
  detections: DetectionItem[]
  persons: PersonItem[]
  faces: FaceStructureItem[]
  vehicles: VehicleItem[]
}

interface AlgorithmSlot {
  emaIntervalMs: number
  lastArrivalMs: number | null
  expiresAtMs: number
  active: ActiveSlotPayload | null
  pendingQueue: PendingAIEvent[]
  personTracks: Map<number, TrackMotionState>
  vehicleTracks: Map<number, TrackMotionState>
}

export interface SampledAIOverlay {
  detections: DetectionItem[]
  persons: PersonItem[]
  faces: FaceStructureItem[]
  vehicles: VehicleItem[]
  hasActiveTargets: boolean
  /** 所有活跃槽位中最早到期的墙钟时刻（毫秒），供视频停帧时兜底定时清屏。 */
  nextExpiryMs: number | null
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}

function clampBox(box: BoxTuple): BoxTuple {
  const x1 = clamp(box[0], 0, 1)
  const y1 = clamp(box[1], 0, 1)
  const x2 = clamp(box[2], x1, 1)
  const y2 = clamp(box[3], y1, 1)
  return [x1, y1, x2, y2]
}

function shiftBox(box: BoxTuple, dx: number, dy: number): BoxTuple {
  if (dx === 0 && dy === 0) return box
  const width = Math.max(0, box[2] - box[0])
  const height = Math.max(0, box[3] - box[1])
  const x1 = clamp(box[0] + dx, 0, Math.max(0, 1 - width))
  const y1 = clamp(box[1] + dy, 0, Math.max(0, 1 - height))
  return clampBox([x1, y1, x1 + width, y1 + height])
}

function boxCenter(box: BoxTuple): [number, number] {
  return [(box[0] + box[2]) * 0.5, (box[1] + box[3]) * 0.5]
}

function boxArea(box: BoxTuple): number {
  return Math.max(0, box[2] - box[0]) * Math.max(0, box[3] - box[1])
}

function easeOutQuad(t: number): number {
  const c = clamp(t, 0, 1)
  return 1 - (1 - c) * (1 - c)
}

function isPlausibleContinuation(prev: BoxTuple, next: BoxTuple): boolean {
  const [cx1, cy1] = boxCenter(prev)
  const [cx2, cy2] = boxCenter(next)
  const dist = Math.hypot(cx2 - cx1, cy2 - cy1)
  if (dist > MAX_SMOOTH_CENTER_JUMP) {
    return false
  }
  const prevArea = boxArea(prev)
  const nextArea = boxArea(next)
  if (prevArea > 1e-6 && nextArea > 1e-6) {
    const ratio = nextArea / prevArea
    if (ratio < MIN_SMOOTH_AREA_RATIO || ratio > MAX_SMOOTH_AREA_RATIO) {
      return false
    }
  }
  return true
}

function resolveSlotKey(event: AIResultEvent): string {
  const algo = event.algorithm_id.trim()
  if (algo.length > 0) return `algo:${algo}`
  const task = event.task_type.trim()
  if (task.length > 0) return `task:${task}`
  return 'default'
}

function computeTtlMs(emaIntervalMs: number): number {
  return clamp(
    Math.round(emaIntervalMs * TTL_MULTIPLIER),
    MIN_TTL_MS,
    MAX_TTL_MS
  )
}

/**
 * 实时 AI 视觉叠加层多算法同步与运动平滑器。
 *
 * 解决实时预览画框的四类核心问题：
 * 1. 按 `algorithm_id` 分槽隔离状态：多算法并发（如人脸 + 车牌 + 安全帽）互不覆盖、互不误清；
 * 2. 主流 `pts_us` 帧级对齐：早于当前呈现视频帧到达的 AI 结果进入有界等待队列，在目标帧上精准释放；
 *    子码流（`sub`）或无 `pts_us` 场景自动无感回退至墙钟驱动；
 * 3. 基于 `track_id` 的受限速度外推 + 残差平滑：消除 5fps 推理叠加在 25/30fps 视频上的跳跃感与滞后感，
 *    跨屏突变自动瞬移（Snap）防拖影；
 * 4. 空目标信标立即清槽 + 自适应消退 TTL：目标离场零残影，正常低帧率推理不闪烁。
 */
export class AIOverlaySynchronizer {
  private readonly slots = new Map<string, AlgorithmSlot>()
  private streamType: StreamType
  private lastVideoPtsUs: number | null = null

  constructor(streamType: StreamType = 'main') {
    this.streamType = streamType
  }

  setStreamType(streamType: StreamType): void {
    if (this.streamType === streamType) return
    this.streamType = streamType
    this.lastVideoPtsUs = null
    // 切换主/子码流时清空早到排队，立即激活最新一帧，防止跨时间轴卡死
    for (const slot of this.slots.values()) {
      slot.pendingQueue = []
    }
  }

  reset(): void {
    this.slots.clear()
    this.lastVideoPtsUs = null
  }

  /**
   * 摄入一条 SSE `ai_result` 事件。
   *
   * @returns 若事件立即生效（未进入早到缓冲队列），返回 true 以便上层可在停帧间隙触发重绘。
   */
  ingest(event: AIResultEvent, nowMs: number): boolean {
    const faces = extractFacesFromEvent(event)
    const hasSpatialTargets =
      event.detections.length > 0 ||
      event.persons.length > 0 ||
      faces.length > 0 ||
      event.vehicles.length > 0

    // 纯图像分类事件无空间几何框，不污染空间叠加层状态槽
    if (!hasSpatialTargets && event.task_type === 'classification') {
      return false
    }

    const key = resolveSlotKey(event)
    let slot = this.slots.get(key)
    if (!slot) {
      slot = {
        emaIntervalMs: DEFAULT_INTERVAL_MS,
        lastArrivalMs: null,
        expiresAtMs: 0,
        active: null,
        pendingQueue: [],
        personTracks: new Map(),
        vehicleTracks: new Map(),
      }
      this.slots.set(key, slot)
    }

    if (slot.lastArrivalMs !== null) {
      const dt = nowMs - slot.lastArrivalMs
      if (dt >= 40 && dt <= 1000) {
        slot.emaIntervalMs = slot.emaIntervalMs * 0.7 + dt * 0.3
      }
    }
    slot.lastArrivalMs = nowMs

    if (this.shouldBufferForVideoPts(event.pts_us)) {
      this.enqueuePending(slot, event, nowMs)
      return false
    }

    // 若队列中有早于本事件的待释放帧，先按序淘汰，直接激活当前最新帧
    if (event.pts_us !== undefined && slot.pendingQueue.length > 0) {
      slot.pendingQueue = slot.pendingQueue.filter(
        (item) =>
          item.event.pts_us !== undefined && item.event.pts_us > event.pts_us!
      )
    } else {
      slot.pendingQueue = []
    }

    this.activateEvent(slot, event, faces, nowMs)
    return true
  }

  /**
   * 在绘制视频帧（传入 `frame.timestamp`）或重绘叠加层时采样当前时刻的融合目标集。
   */
  sample(nowMs: number, videoPtsUs?: number | null): SampledAIOverlay {
    if (videoPtsUs !== undefined && videoPtsUs !== null) {
      if (
        this.lastVideoPtsUs !== null &&
        Math.abs(videoPtsUs - this.lastVideoPtsUs) > MAX_PTS_SKEW_US
      ) {
        // 视频时间轴大幅跳变（重连/重启）：释放旧时间轴上的排队事件
        for (const slot of this.slots.values()) {
          this.flushAllPending(slot, nowMs)
        }
      }
      this.lastVideoPtsUs = videoPtsUs
    }

    const detections: DetectionItem[] = []
    const persons: PersonItem[] = []
    const faces: FaceStructureItem[] = []
    const vehicles: VehicleItem[] = []
    let nextExpiryMs: number | null = null

    for (const slot of this.slots.values()) {
      this.promoteDuePending(slot, nowMs)

      if (!slot.active) {
        continue
      }

      if (nowMs >= slot.expiresAtMs) {
        slot.active = null
        slot.personTracks.clear()
        slot.vehicleTracks.clear()
        continue
      }

      if (nextExpiryMs === null || slot.expiresAtMs < nextExpiryMs) {
        nextExpiryMs = slot.expiresAtMs
      }

      const active = slot.active
      detections.push(...active.detections)

      // 1. 人体框平滑及关联人脸同步位移
      const personDeltas: Array<[number, number]> = new Array(
        active.persons.length
      )
      for (let i = 0; i < active.persons.length; i++) {
        const rawPerson = active.persons[i]
        const rawBox = rawPerson.body_box
        let smoothedBox = rawBox
        if (rawPerson.track_id > 0) {
          const track = slot.personTracks.get(rawPerson.track_id)
          if (track) {
            smoothedBox = this.evaluateTrackBox(
              track,
              slot.emaIntervalMs,
              nowMs
            )
          }
        }
        const [rawCx, rawCy] = boxCenter(rawBox)
        const [smCx, smCy] = boxCenter(smoothedBox)
        const dx = smCx - rawCx
        const dy = smCy - rawCy
        personDeltas[i] = [dx, dy]

        if (dx === 0 && dy === 0 && smoothedBox === rawBox) {
          persons.push(rawPerson)
        } else {
          persons.push({
            ...rawPerson,
            body_box: smoothedBox,
            faces:
              rawPerson.faces.length > 0
                ? rawPerson.faces.map((f) => ({
                    ...f,
                    box: shiftBox(f.box, dx, dy),
                    landmarks:
                      f.landmarks.length > 0
                        ? f.landmarks.map(([lx, ly]) => [
                            clamp(lx + dx, 0, 1),
                            clamp(ly + dy, 0, 1),
                          ])
                        : f.landmarks,
                  }))
                : rawPerson.faces,
          })
        }
      }

      // 2. 顶层人脸框（关联人体跟随父框平滑位移，孤立人脸直接绘制）
      for (const rawFace of active.faces) {
        const pIdx = rawFace.person_index
        if (pIdx >= 0 && pIdx < personDeltas.length) {
          const [dx, dy] = personDeltas[pIdx]
          if (dx !== 0 || dy !== 0) {
            faces.push({
              ...rawFace,
              box: shiftBox(rawFace.box, dx, dy),
              landmarks:
                rawFace.landmarks.length > 0
                  ? rawFace.landmarks.map(([lx, ly]) => [
                      clamp(lx + dx, 0, 1),
                      clamp(ly + dy, 0, 1),
                    ])
                  : rawFace.landmarks,
            })
            continue
          }
        }
        faces.push(rawFace)
      }

      // 3. 机动车框平滑及挂载车牌同步位移
      for (const rawVehicle of active.vehicles) {
        const rawBox = rawVehicle.vehicle_box
        let smoothedBox = rawBox
        if (rawVehicle.track_id > 0) {
          const track = slot.vehicleTracks.get(rawVehicle.track_id)
          if (track) {
            smoothedBox = this.evaluateTrackBox(
              track,
              slot.emaIntervalMs,
              nowMs
            )
          }
        }
        const [rawCx, rawCy] = boxCenter(rawBox)
        const [smCx, smCy] = boxCenter(smoothedBox)
        const dx = smCx - rawCx
        const dy = smCy - rawCy

        if (dx === 0 && dy === 0 && smoothedBox === rawBox) {
          vehicles.push(rawVehicle)
        } else {
          const shiftedPlates: PlateSubItem[] =
            rawVehicle.plates.length > 0
              ? rawVehicle.plates.map((plate) => ({
                  ...plate,
                  box: shiftBox(plate.box, dx, dy),
                }))
              : rawVehicle.plates
          vehicles.push({
            ...rawVehicle,
            vehicle_box: smoothedBox,
            plates: shiftedPlates,
          })
        }
      }
    }

    const hasActiveTargets =
      detections.length > 0 ||
      persons.length > 0 ||
      faces.length > 0 ||
      vehicles.length > 0

    return {
      detections,
      persons,
      faces,
      vehicles,
      hasActiveTargets,
      nextExpiryMs,
    }
  }

  private shouldBufferForVideoPts(eventPtsUs: number | undefined): boolean {
    if (this.streamType !== 'main') return false
    if (eventPtsUs === undefined || this.lastVideoPtsUs === null) return false
    if (Math.abs(eventPtsUs - this.lastVideoPtsUs) > MAX_PTS_SKEW_US) {
      return false
    }
    return eventPtsUs > this.lastVideoPtsUs + PTS_EARLY_TOLERANCE_US
  }

  private enqueuePending(
    slot: AlgorithmSlot,
    event: AIResultEvent,
    nowMs: number
  ): void {
    slot.pendingQueue.push({ event, enqueuedAtMs: nowMs })
    slot.pendingQueue.sort(
      (a, b) => (a.event.pts_us ?? 0) - (b.event.pts_us ?? 0)
    )
    while (slot.pendingQueue.length > MAX_PENDING_PER_SLOT) {
      slot.pendingQueue.shift()
    }
  }

  private promoteDuePending(slot: AlgorithmSlot, nowMs: number): void {
    if (slot.pendingQueue.length === 0) return

    let promotedCount = 0
    for (const item of slot.pendingQueue) {
      const ptsUs = item.event.pts_us
      const dueByPts =
        this.streamType === 'main' &&
        this.lastVideoPtsUs !== null &&
        ptsUs !== undefined &&
        (ptsUs <= this.lastVideoPtsUs + PTS_EARLY_TOLERANCE_US ||
          Math.abs(ptsUs - this.lastVideoPtsUs) > MAX_PTS_SKEW_US)
      const dueByHoldTimeout = nowMs - item.enqueuedAtMs >= MAX_PENDING_HOLD_MS

      if (dueByPts || dueByHoldTimeout) {
        const faces = extractFacesFromEvent(item.event)
        this.activateEvent(slot, item.event, faces, nowMs)
        promotedCount++
      } else {
        break
      }
    }

    if (promotedCount > 0) {
      slot.pendingQueue.splice(0, promotedCount)
    }
  }

  private flushAllPending(slot: AlgorithmSlot, nowMs: number): void {
    if (slot.pendingQueue.length === 0) return
    const latest = slot.pendingQueue[slot.pendingQueue.length - 1]
    slot.pendingQueue = []
    if (latest) {
      this.activateEvent(
        slot,
        latest.event,
        extractFacesFromEvent(latest.event),
        nowMs
      )
    }
  }

  private activateEvent(
    slot: AlgorithmSlot,
    event: AIResultEvent,
    faces: FaceStructureItem[],
    nowMs: number
  ): void {
    const isEmpty =
      event.detections.length === 0 &&
      event.persons.length === 0 &&
      faces.length === 0 &&
      event.vehicles.length === 0

    if (isEmpty) {
      // 显式空目标信标：目标已离开画面，立即清空该算法槽位，零残影滞留
      slot.active = null
      slot.expiresAtMs = 0
      slot.personTracks.clear()
      slot.vehicleTracks.clear()
      return
    }

    this.updateTrackMap(
      slot.personTracks,
      event.persons,
      (p) => p.track_id,
      (p) => p.body_box,
      event.pts_us,
      slot.emaIntervalMs,
      nowMs
    )
    this.updateTrackMap(
      slot.vehicleTracks,
      event.vehicles,
      (v) => v.track_id,
      (v) => v.vehicle_box,
      event.pts_us,
      slot.emaIntervalMs,
      nowMs
    )

    slot.active = {
      activatedAtMs: nowMs,
      ptsUs: event.pts_us,
      detections: event.detections,
      persons: event.persons,
      faces,
      vehicles: event.vehicles,
    }
    slot.expiresAtMs = nowMs + computeTtlMs(slot.emaIntervalMs)
  }

  private updateTrackMap<T>(
    trackMap: Map<number, TrackMotionState>,
    items: T[],
    getId: (item: T) => number,
    getBox: (item: T) => BoxTuple,
    eventPtsUs: number | undefined,
    emaIntervalMs: number,
    nowMs: number
  ): void {
    const seenIds = new Set<number>()

    for (const item of items) {
      const trackId = getId(item)
      if (trackId <= 0) continue
      seenIds.add(trackId)

      const nextRaw = clampBox(getBox(item))
      const prev = trackMap.get(trackId)
      if (!prev || !isPlausibleContinuation(prev.rawBox, nextRaw)) {
        trackMap.set(trackId, {
          rawBox: nextRaw,
          rawAtMs: nowMs,
          rawPtsUs: eventPtsUs,
          velPerMs: [0, 0, 0, 0],
          hasVelocity: false,
          blendOffset: [0, 0, 0, 0],
        })
        continue
      }

      const prevRendered = this.evaluateTrackBox(prev, emaIntervalMs, nowMs)

      let dtMs = nowMs - prev.rawAtMs
      if (
        eventPtsUs !== undefined &&
        prev.rawPtsUs !== undefined &&
        eventPtsUs > prev.rawPtsUs
      ) {
        const mediaDtMs = (eventPtsUs - prev.rawPtsUs) / 1000
        if (mediaDtMs >= 25 && mediaDtMs <= 1000) {
          dtMs = mediaDtMs
        }
      }

      let nextVel: BoxTuple = prev.velPerMs
      let hasVelocity = prev.hasVelocity
      if (dtMs >= 25 && dtMs <= 1000) {
        const instVel: BoxTuple = [
          ((nextRaw[0] - prev.rawBox[0]) / dtMs) * VELOCITY_DAMPING,
          ((nextRaw[1] - prev.rawBox[1]) / dtMs) * VELOCITY_DAMPING,
          ((nextRaw[2] - prev.rawBox[2]) / dtMs) * VELOCITY_DAMPING,
          ((nextRaw[3] - prev.rawBox[3]) / dtMs) * VELOCITY_DAMPING,
        ]
        nextVel = hasVelocity
          ? [
              prev.velPerMs[0] * 0.3 + instVel[0] * 0.7,
              prev.velPerMs[1] * 0.3 + instVel[1] * 0.7,
              prev.velPerMs[2] * 0.3 + instVel[2] * 0.7,
              prev.velPerMs[3] * 0.3 + instVel[3] * 0.7,
            ]
          : instVel
        hasVelocity = true
      }

      const nextState: TrackMotionState = {
        rawBox: nextRaw,
        rawAtMs: nowMs,
        rawPtsUs: eventPtsUs,
        velPerMs: nextVel,
        hasVelocity,
        blendOffset: [0, 0, 0, 0],
      }
      // 计算在激活瞬间（wallElapsed = 0）的基础预测框，使残差偏移保持严格 C0 连续
      const baseAtNow = this.evaluateTrackBox(nextState, emaIntervalMs, nowMs)
      nextState.blendOffset = [
        prevRendered[0] - baseAtNow[0],
        prevRendered[1] - baseAtNow[1],
        prevRendered[2] - baseAtNow[2],
        prevRendered[3] - baseAtNow[3],
      ]
      trackMap.set(trackId, nextState)
    }

    for (const existingId of trackMap.keys()) {
      if (!seenIds.has(existingId)) {
        trackMap.delete(existingId)
      }
    }
  }

  private evaluateTrackBox(
    track: TrackMotionState,
    emaIntervalMs: number,
    nowMs: number
  ): BoxTuple {
    let leadMs: number
    if (
      this.streamType === 'main' &&
      track.rawPtsUs !== undefined &&
      this.lastVideoPtsUs !== null &&
      Math.abs(this.lastVideoPtsUs - track.rawPtsUs) <= 500_000
    ) {
      leadMs = Math.max(0, (this.lastVideoPtsUs - track.rawPtsUs) / 1000)
    } else {
      leadMs = Math.max(0, nowMs - track.rawAtMs)
    }

    const maxExtrapMs = Math.min(emaIntervalMs * 1.15, MAX_EXTRAPOLATION_MS)
    const extrapMs = Math.min(leadMs, maxExtrapMs)

    const wallElapsed = Math.max(0, nowMs - track.rawAtMs)
    const blendFactor =
      wallElapsed >= BLEND_DURATION_MS
        ? 0
        : 1 - easeOutQuad(wallElapsed / BLEND_DURATION_MS)

    const dx1 = clamp(
      track.velPerMs[0] * extrapMs,
      -MAX_EXTRAP_SHIFT,
      MAX_EXTRAP_SHIFT
    )
    const dy1 = clamp(
      track.velPerMs[1] * extrapMs,
      -MAX_EXTRAP_SHIFT,
      MAX_EXTRAP_SHIFT
    )
    const dx2 = clamp(
      track.velPerMs[2] * extrapMs,
      -MAX_EXTRAP_SHIFT,
      MAX_EXTRAP_SHIFT
    )
    const dy2 = clamp(
      track.velPerMs[3] * extrapMs,
      -MAX_EXTRAP_SHIFT,
      MAX_EXTRAP_SHIFT
    )

    return clampBox([
      track.rawBox[0] + dx1 + track.blendOffset[0] * blendFactor,
      track.rawBox[1] + dy1 + track.blendOffset[1] * blendFactor,
      track.rawBox[2] + dx2 + track.blendOffset[2] * blendFactor,
      track.rawBox[3] + dy2 + track.blendOffset[3] * blendFactor,
    ])
  }
}
