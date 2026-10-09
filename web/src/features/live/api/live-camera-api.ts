import { apiClient, type ApiResponse } from '@/lib/api-client'
import { type LiveCameraItem } from '../types'

export interface BackendStreamResponse {
  id: number
  role: 'main' | 'sub'
  protocol: string
  rtspUrl?: string
  codec: string
  width: number
  height: number
  fps?: number
}

export interface BackendCameraResponse {
  id: string
  name: string
  enabled: boolean
  health: string
  session: string
  streams: BackendStreamResponse[]
}

export interface BackendCameraListResponse {
  items: BackendCameraResponse[]
  total: number
}

/**
 * 生成相机的 WebSocket 流地址
 */
export function getCameraStreamWsUrl(
  cameraId: string,
  streamType: 'main' | 'sub' = 'main'
): string {
  const protocol =
    typeof window !== 'undefined' && window.location.protocol === 'https:'
      ? 'wss:'
      : 'ws:'
  const host =
    typeof window !== 'undefined' ? window.location.host : 'localhost'
  return `${protocol}//${host}/api/v1/cameras/${encodeURIComponent(
    cameraId
  )}/streams/${streamType}/ws`
}

function resolveCameraStatus(
  health: string,
  session: string
): LiveCameraItem['status'] {
  if (health === 'online') return 'online'
  if (health === 'error') return 'error'
  if (session === 'reconnecting') return 'reconnecting'
  return 'offline'
}

/**
 * 请求后端真实摄像机资产列表，并适配为前端监控视图所需格式
 */
export async function fetchLiveCameras(): Promise<LiveCameraItem[]> {
  const resp =
    await apiClient.get<ApiResponse<BackendCameraListResponse>>('/cameras')
  const backendCameras = resp.data.data?.items ?? []

  return backendCameras.map((cam) => {
    const mainStream = cam.streams?.find((s) => s.role === 'main')
    const subStream = cam.streams?.find((s) => s.role === 'sub')
    const codecStr = mainStream?.codec?.toLowerCase() ?? ''
    const isH265 = codecStr.includes('265') || codecStr.includes('hevc')
    const resolution =
      mainStream?.width && mainStream?.height
        ? `${mainStream.width}x${mainStream.height}`
        : '1920x1080'

    // 智能从 rtspUrl 中提取目标主机 IP
    const ipMatch = mainStream?.rtspUrl?.match(/@([^:/]+)/)
    const ip = ipMatch ? ipMatch[1] : '127.0.0.1'

    return {
      id: cam.id,
      name: cam.name,
      ip,
      status: resolveCameraStatus(cam.health, cam.session),
      hasSubStream: Boolean(subStream),
      codec: isH265 ? 'h265' : 'h264',
      resolution,
    }
  })
}
