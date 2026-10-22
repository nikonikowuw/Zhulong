import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import { type LiveCameraItem } from '../types'

const backendStreamResponseSchema = z.object({
  id: z.number().int().nonnegative(),
  role: z.enum(['main', 'sub']),
  protocol: z.string(),
  rtspUrl: z.string().optional(),
  /** 后端解析并脱敏后提供；无法解析时为空串 */
  host: z.string().optional(),
  codec: z.string(),
  width: z.number().int().nonnegative(),
  height: z.number().int().nonnegative(),
  fps: z.number().nullable().optional(),
})

const backendCameraResponseSchema = z.object({
  id: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  health: z.string(),
  session: z.string(),
  streams: z.array(backendStreamResponseSchema),
})

export const backendCameraListResponseSchema = z.object({
  items: z.array(backendCameraResponseSchema),
  total: z.number().int().nonnegative(),
})

const cameraListEnvelopeSchema = z.object({
  code: z.literal('OK'),
  message: z.string(),
  data: z.unknown(),
})

export type BackendStreamResponse = z.infer<typeof backendStreamResponseSchema>
export type BackendCameraResponse = z.infer<typeof backendCameraResponseSchema>
export type BackendCameraListResponse = z.infer<
  typeof backendCameraListResponseSchema
>

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
 * 归一化后端下发的主机字段：空串与仅空白一律视为「未知」
 */
function normalizeHost(host?: string): string | undefined {
  const trimmed = host?.trim()
  return trimmed ? trimmed.toLowerCase() : undefined
}

/**
 * 请求后端真实摄像机资产列表，并适配为前端监控视图所需格式
 */
export async function fetchLiveCameras(): Promise<LiveCameraItem[]> {
  const resp = await apiClient.get<unknown>('/cameras')
  const envelope = cameraListEnvelopeSchema.parse(resp.data)
  const { items: backendCameras } = backendCameraListResponseSchema.parse(
    envelope.data
  )

  return backendCameras.map((cam) => {
    const mainStream = cam.streams?.find((s) => s.role === 'main')
    const subStream = cam.streams?.find((s) => s.role === 'sub')
    const codecStr = mainStream?.codec?.toLowerCase() ?? ''
    const isH265 = codecStr.includes('265') || codecStr.includes('hevc')
    // 分辨率与主机均只在后端提供真实数据时才填充，缺失即留空由 UI 兜底
    const resolution =
      mainStream?.width && mainStream?.height
        ? `${mainStream.width}x${mainStream.height}`
        : undefined

    const host =
      normalizeHost(mainStream?.host) ?? normalizeHost(subStream?.host)

    return {
      id: cam.id,
      name: cam.name,
      host,
      status: resolveCameraStatus(cam.health, cam.session),
      hasSubStream: Boolean(subStream),
      codec: isH265 ? 'h265' : 'h264',
      resolution,
    }
  })
}
