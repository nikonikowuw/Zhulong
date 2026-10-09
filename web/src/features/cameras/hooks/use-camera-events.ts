import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  type Camera,
  type CameraListResponse,
  type CameraStateInfo,
} from '../data/schema'
import { CAMERAS_QUERY_KEY } from './use-cameras'

interface SSEMessage<T = unknown> {
  event: string
  sequence?: number
  data: T
}

/**
 * 监听后端 /api/v1/cameras/events SSE 实时状态事件
 * 实现零轮询的在线/推流/降级毫秒级实时响应
 */
export function useCameraEvents(enabled = true) {
  const queryClient = useQueryClient()
  const eventSourceRef = useRef<EventSource | null>(null)
  const reconnectTimeoutRef = useRef<number | null>(null)
  const retryCountRef = useRef(0)

  useEffect(() => {
    if (!enabled) return

    let isUnmounted = false

    const connect = () => {
      if (isUnmounted) return

      try {
        const es = new EventSource('/api/v1/cameras/events', {
          withCredentials: true,
        })
        eventSourceRef.current = es

        es.onopen = () => {
          retryCountRef.current = 0
        }

        // 处理 initial snapshot 全量快照
        es.addEventListener('snapshot', (e: MessageEvent) => {
          try {
            const rawData = JSON.parse(e.data) as CameraStateInfo[]
            if (!Array.isArray(rawData)) return

            const stateMap = new Map<string, CameraStateInfo>()
            rawData.forEach((item) => {
              if (item.cameraId) {
                stateMap.set(item.cameraId, item)
              }
            })

            // 更新缓存中的所有匹配相机状态
            queryClient.setQueriesData<CameraListResponse>(
              { queryKey: CAMERAS_QUERY_KEY },
              (old) => {
                if (!old?.items) return old
                return {
                  ...old,
                  items: old.items.map((cam: Camera) => {
                    const matchedState = stateMap.get(cam.id)
                    if (!matchedState) return cam
                    return {
                      ...cam,
                      health:
                        (matchedState.health as Camera['health']) || cam.health,
                      session:
                        (matchedState.session as Camera['session']) ||
                        cam.session,
                      degraded: matchedState.degraded ?? cam.degraded,
                      stale: matchedState.stale ?? cam.stale,
                      reason: matchedState.reason ?? cam.reason,
                      lastCheckedAt:
                        matchedState.lastCheckedAt ?? cam.lastCheckedAt,
                      lastSuccessAt:
                        matchedState.lastSuccessAt ?? cam.lastSuccessAt,
                    }
                  }),
                }
              }
            )
          } catch {
            // 解析异常静默忽略
          }
        })

        // 处理增量 change 事件
        es.addEventListener('change', (e: MessageEvent) => {
          try {
            const state = JSON.parse(e.data) as CameraStateInfo
            if (!state?.cameraId) return

            queryClient.setQueriesData<CameraListResponse>(
              { queryKey: CAMERAS_QUERY_KEY },
              (old) => {
                if (!old?.items) return old
                return {
                  ...old,
                  items: old.items.map((cam: Camera) => {
                    if (cam.id !== state.cameraId) return cam
                    return {
                      ...cam,
                      health: (state.health as Camera['health']) || cam.health,
                      session:
                        (state.session as Camera['session']) || cam.session,
                      degraded: state.degraded ?? cam.degraded,
                      stale: state.stale ?? cam.stale,
                      reason: state.reason ?? cam.reason,
                      lastCheckedAt: state.lastCheckedAt ?? cam.lastCheckedAt,
                      lastSuccessAt: state.lastSuccessAt ?? cam.lastSuccessAt,
                    }
                  }),
                }
              }
            )
          } catch {
            // 解析异常静默忽略
          }
        })

        // 统一消息回退监听
        es.onmessage = (e: MessageEvent) => {
          try {
            const parsed = JSON.parse(e.data) as SSEMessage<CameraStateInfo>
            if (parsed.event === 'change' && parsed.data?.cameraId) {
              const state = parsed.data
              queryClient.setQueriesData<CameraListResponse>(
                { queryKey: CAMERAS_QUERY_KEY },
                (old) => {
                  if (!old?.items) return old
                  return {
                    ...old,
                    items: old.items.map((cam: Camera) => {
                      if (cam.id !== state.cameraId) return cam
                      return {
                        ...cam,
                        health:
                          (state.health as Camera['health']) || cam.health,
                        session:
                          (state.session as Camera['session']) || cam.session,
                        degraded: state.degraded ?? cam.degraded,
                        stale: state.stale ?? cam.stale,
                        reason: state.reason ?? cam.reason,
                      }
                    }),
                  }
                }
              )
            }
          } catch {
            // 忽略心跳或无法解析的报文
          }
        }

        es.onerror = () => {
          es.close()
          eventSourceRef.current = null

          if (!isUnmounted) {
            // 指数退避重连: 1s, 2s, 4s, 8s, 最大 15s
            const delay = Math.min(
              1000 * Math.pow(2, retryCountRef.current),
              15000
            )
            retryCountRef.current += 1
            reconnectTimeoutRef.current = window.setTimeout(connect, delay)
          }
        }
      } catch {
        // 环境不支持或网络异常降级
      }
    }

    connect()

    return () => {
      isUnmounted = true
      if (reconnectTimeoutRef.current !== null) {
        clearTimeout(reconnectTimeoutRef.current)
      }
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
        eventSourceRef.current = null
      }
    }
  }, [enabled, queryClient])
}
