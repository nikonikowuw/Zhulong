import { type OverlayPaintOptions } from './overlay-paint'

/** 主线程 → 导出 Worker：一条图片任务（全部字段可结构化克隆） */
export interface ImageWorkerRequest {
  id: number
  /** 记录域相对路径、绝对 URL、data: 或 blob: */
  imagePath: string
  /** null 表示只拉原图、不烧录 */
  overlay: OverlayPaintOptions | null
  mimeType: string
  quality: number
}

/** 导出 Worker → 主线程：成功时回传编码后的图片字节 */
export type ImageWorkerResponse =
  | { id: number; ok: true; bytes: ArrayBuffer }
  | {
      id: number
      ok: false
      message: string
      /** 原始 HTTP 状态码，供主线程还原 `MediaFetchError` 的重试语义 */
      status?: number
      /** 运行环境不支持（如 Worker 内没有 OffscreenCanvas），应整体回退主线程 */
      unsupported?: boolean
    }
