import { useEffect, useRef, useState, useCallback } from 'react'
import { AIResultService } from '../services/ai-result-service'
import {
  extractFacesFromEvent,
  type AIResultEvent,
  type FaceSubItem,
} from '../types/ai-result'

interface UseAIResultsOptions {
  cameraId?: string | null
  enabled?: boolean
  fadeTimeoutMs?: number
}

/**
 * 订阅指定相机的实时 AI 感知结果 Hook
 * 返回最新的人脸检测/识别结果，并提供自动消退计时（默认 350ms 无更新则清空），杜绝幽灵框。
 *
 * ⚠️ 性能与架构约束：
 * 本 Hook 内部使用 useState 响应推理结果，适合低频卡片、详情抽屉或调试工具消费。
 * 严禁在视频画面高频叠加层中使用本 Hook。实时叠加层必须严格遵循《AI 视觉叠加层：零 React 重渲染》
 * 铁律，通过 AIResultService 单例配合 mutable ref 与 Canvas 2D / rAF 循环直接绘制。
 */
export function useAIResults({
  cameraId,
  enabled = true,
  fadeTimeoutMs = 350,
}: UseAIResultsOptions) {
  const [latestResult, setLatestResult] = useState<AIResultEvent | null>(null)
  const [faces, setFaces] = useState<FaceSubItem[]>([])
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const clearFaces = useCallback(() => {
    setLatestResult(null)
    setFaces([])
  }, [])

  useEffect(() => {
    if (!enabled || !cameraId) {
      return
    }

    const service = AIResultService.getInstance()
    const unsubscribe = service.subscribeCamera(cameraId, (event) => {
      // 提取结构化人脸项（兼容 SCENE_STRUCTURE v2 顶层 faces 与旧版嵌套结构）
      const extractedFaces = extractFacesFromEvent(event)

      setLatestResult(event)
      setFaces(extractedFaces)

      // 重置消退计时器
      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
      }
      fadeTimerRef.current = setTimeout(() => {
        setFaces([])
        fadeTimerRef.current = null
      }, fadeTimeoutMs)
    })

    return () => {
      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
        fadeTimerRef.current = null
      }
      unsubscribe()
      setLatestResult(null)
      setFaces([])
    }
  }, [cameraId, enabled, fadeTimeoutMs])

  return {
    latestResult,
    faces,
    clearFaces,
  }
}
