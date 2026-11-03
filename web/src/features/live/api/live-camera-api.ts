import { type Camera } from '@/features/cameras/contract'
import { type LiveCameraItem } from '../types'

/**
 * 推导当前页面的 WebSocket 基地址（协议 + 主机）。
 *
 * 视频与音频两条 WS 必须落在同一个源上：同源才能带 cookie，也才能让
 * 后面的网关鉴权/限流按同一条会话计数。因此协议与主机的推导只许有一份。
 */
function wsBase(): string {
  const protocol =
    typeof window !== 'undefined' && window.location.protocol === 'https:'
      ? 'wss:'
      : 'ws:'
  const host =
    typeof window !== 'undefined' ? window.location.host : 'localhost'
  return `${protocol}//${host}`
}

/**
 * 生成相机的 WebSocket 流地址
 */
export function getCameraStreamWsUrl(
  cameraId: string,
  streamType: 'main' | 'sub' = 'main'
): string {
  return `${wsBase()}/api/v1/cameras/${encodeURIComponent(
    cameraId
  )}/streams/${streamType}/ws`
}

/**
 * 生成相机的按需音频通道地址。
 *
 * 音频是**独立连接**而不是预览 WS 上的第二条轨：预览 WS 的二进制帧必须逐字节
 * 保持不变（旧 bundle 不能因为「多了一条音轨」而黑屏），而且音频的背压、重连节奏
 * 与视频完全不同，混在一条连接上只会互相拖累。
 */
export function getCameraAudioWsUrl(
  cameraId: string,
  streamType: 'main' | 'sub' = 'main'
): string {
  return `${wsBase()}/api/v1/cameras/${encodeURIComponent(
    cameraId
  )}/streams/${streamType}/audio/ws`
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
 * 把后端相机契约适配为监控视图所需的展示模型。
 *
 * 纯函数，不发起请求：数据来源固定为 `/cameras`，由 `useAllCameras` 统一托管，
 * SSE 增量通过 `setQueriesData` 直接 patch 缓存，因此这里只负责字段派生。
 * 缺失的分辨率与主机一律留空，绝不填充占位值。
 */
export function toLiveCameraItem(cam: Camera): LiveCameraItem {
  const mainStream = cam.streams?.find((s) => s.role === 'main')
  const subStream = cam.streams?.find((s) => s.role === 'sub')
  const codecStr = mainStream?.codec?.toLowerCase() ?? ''
  const isH265 = codecStr.includes('265') || codecStr.includes('hevc')
  const resolution =
    mainStream?.width && mainStream?.height
      ? `${mainStream.width}x${mainStream.height}`
      : undefined

  return {
    id: cam.id,
    name: cam.name,
    host: normalizeHost(mainStream?.host) ?? normalizeHost(subStream?.host),
    status: resolveCameraStatus(cam.health, cam.session),
    hasSubStream: Boolean(subStream),
    codec: isH265 ? 'h265' : 'h264',
    resolution,
  }
}
