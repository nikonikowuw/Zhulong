import { useEffect, useRef, useState, useCallback } from 'react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEvent, type FaceSubItem } from '../types/ai-result'

interface UseAIResultsOptions {
  cameraId?: string | null
  enabled?: boolean
  fadeTimeoutMs?: number
}

/**
 * 订阅指定相机的实时 AI 感知结果 Hook
 * 返回最新的人脸检测/识别结果，并提供自动消退计时（默认 350ms 无更新则清空），杜绝幽灵框。
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
      // 提取结构化人脸项
      const extractedFaces: FaceSubItem[] = []
      if (event.persons) {
        for (const p of event.persons) {
          if (p.faces && p.faces.length > 0) {
            extractedFaces.push(...p.faces)
          }
        }
      }

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
