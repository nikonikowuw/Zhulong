import { useEffect } from 'react'
import { z } from 'zod'
import { type QueryClient, useQueryClient } from '@tanstack/react-query'
import {
  cameraStateInfoSchema,
  type Camera,
  type CameraListResponse,
  type CameraStateInfo,
} from '../data/schema'
import { CAMERAS_QUERY_KEY } from './use-cameras'

/**
 * `snapshot` 载荷是以 cameraId 为键的对象（Go 侧 `map[string]*CameraStateInfo`）。
 * 数组形态保留兼容，避免后端哪天改回列表时再次静默失效。
 */
const snapshotPayloadSchema = z.union([
  z.record(z.string(), cameraStateInfoSchema),
  z.array(cameraStateInfoSchema),
])

function patchCamera(cam: Camera, state: CameraStateInfo): Camera {
  return {
    ...cam,
    health: (state.health as Camera['health']) || cam.health,
    session: (state.session as Camera['session']) || cam.session,
    degraded: state.degraded ?? cam.degraded,
    stale: state.stale ?? cam.stale,
    reason: state.reason ?? cam.reason,
    lastCheckedAt: state.lastCheckedAt ?? cam.lastCheckedAt,
    lastSuccessAt: state.lastSuccessAt ?? cam.lastSuccessAt,
  }
}

function applySnapshot(queryClient: QueryClient, payload: unknown) {
  const parseResult = snapshotPayloadSchema.safeParse(payload)
  if (!parseResult.success) return

  const stateMap = new Map<string, CameraStateInfo>()
  if (Array.isArray(parseResult.data)) {
    for (const item of parseResult.data) {
      if (item.cameraId) stateMap.set(item.cameraId, item)
    }
  } else {
    for (const [key, item] of Object.entries(parseResult.data)) {
      stateMap.set(item.cameraId || key, item)
    }
  }

  // 1. 更新所有匹配列表查询缓存（包括 ['cameras', 'all'] 与分页列表）
  queryClient.setQueriesData<CameraListResponse>(
    { queryKey: CAMERAS_QUERY_KEY },
    (old) => {
      if (!old?.items) return old
      return {
        ...old,
        items: old.items.map((cam: Camera) => {
          const matched = stateMap.get(cam.id)
          return matched ? patchCamera(cam, matched) : cam
        }),
      }
    }
  )

  // 2. 更新单个相机详情缓存
  for (const [id, state] of stateMap.entries()) {
    queryClient.setQueryData<Camera>(
      [...CAMERAS_QUERY_KEY, 'detail', id],
      (old) => (old ? patchCamera(old, state) : old)
    )
  }
}

function applyChange(queryClient: QueryClient, payload: unknown) {
  const parseResult = cameraStateInfoSchema.safeParse(payload)
  if (!parseResult.success) return
  const state = parseResult.data

  // 1. 更新所有匹配列表查询缓存
  queryClient.setQueriesData<CameraListResponse>(
    { queryKey: CAMERAS_QUERY_KEY },
    (old) => {
      if (!old?.items) return old
      return {
        ...old,
        items: old.items.map((cam: Camera) =>
          cam.id === state.cameraId ? patchCamera(cam, state) : cam
        ),
      }
    }
  )

  // 2. 更新单个相机详情缓存
  queryClient.setQueryData<Camera>(
    [...CAMERAS_QUERY_KEY, 'detail', state.cameraId],
    (old) => (old ? patchCamera(old, state) : old)
  )
}

type SSEListener = (event: 'snapshot' | 'change', data: unknown) => void

/**
 * 模块级引用计数单例事件连接池。
 * 确保单 Tab 内即使多处挂载 Hook 也仅维持 1 条 EventSource，
 * 避免耗尽浏览器持久连接池（HTTP/1.1 最大 6 条）与后端客户端上限（SSEMaxClients = 16）。
 */
class CameraEventsManager {
  private subscribers = new Set<SSEListener>()
  private es: EventSource | null = null
  private retryCount = 0
  private reconnectTimer: number | null = null

  subscribe(listener: SSEListener): () => void {
    this.subscribers.add(listener)

    if (!this.es && this.reconnectTimer === null) {
      this.connect()
    }

    return () => {
      this.subscribers.delete(listener)
      if (this.subscribers.size === 0) {
        this.disconnect()
      }
    }
  }

  private notify(event: 'snapshot' | 'change', data: unknown) {
    for (const listener of this.subscribers) {
      listener(event, data)
    }
  }

  private connect() {
    if (typeof window === 'undefined' || typeof EventSource === 'undefined') {
      return
    }

    try {
      const es = new EventSource('/api/v1/cameras/events', {
        withCredentials: true,
      })
      this.es = es

      es.onopen = () => {
        this.retryCount = 0
      }

      es.addEventListener('snapshot', (e: MessageEvent) => {
        try {
          const json = JSON.parse(e.data)
          this.notify('snapshot', json)
        } catch {
          // ignore parsing error
        }
      })

      es.addEventListener('change', (e: MessageEvent) => {
        try {
          const json = JSON.parse(e.data)
          this.notify('change', json)
        } catch {
          // ignore parsing error
        }
      })

      es.onerror = () => {
        es.close()
        this.es = null

        if (this.subscribers.size > 0) {
          const delay = Math.min(1000 * Math.pow(2, this.retryCount), 15000)
          this.retryCount += 1
          this.reconnectTimer = window.setTimeout(() => {
            this.reconnectTimer = null
            this.connect()
          }, delay)
        }
      }
    } catch {
      // 环境不支持或网络降级
    }
  }

  private disconnect() {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.es) {
      this.es.close()
      this.es = null
    }
    this.retryCount = 0
  }
}

const cameraEventsManager = new CameraEventsManager()

/**
 * 监听后端 /api/v1/cameras/events SSE 实时状态事件。
 * 共享单例 EventSource，零轮询实现毫秒级状态同步。
 */
export function useCameraEvents(enabled = true) {
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!enabled) return

    const handleEvent: SSEListener = (event, payload) => {
      if (event === 'snapshot') {
        applySnapshot(queryClient, payload)
      } else if (event === 'change') {
        applyChange(queryClient, payload)
      }
    }

    return cameraEventsManager.subscribe(handleEvent)
  }, [enabled, queryClient])
}
