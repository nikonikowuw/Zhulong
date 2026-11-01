import { format } from 'date-fns'

/**
 * 证据图下载到本地时使用的文件名。
 *
 * 落地路径是 `snapshots/{cameraId}/{YYYY-MM-DD}/{id}.jpg`（见
 * `internal/record/service.go`），名字只有裸 ULID；`internal/mediafile` 又刻意
 * 不下发 `Content-Disposition` —— 那会强制下载、破坏 `<img>` 的内联预览。两头
 * 都不负责，所以「用户硬盘上这张图叫什么」完全由前端的 `download` 属性决定。
 *
 * 这里补的是**抓拍时刻**，而不是类别前缀：ULID 时间有序但对人不可读，把它从
 * 下载目录里捞出来时唯一想知道的恰恰是「这是什么时候拍的」。补上时间戳之后，
 * 文件名按字典序排列即等于按时间排列。
 *
 * @param id 记录 ID（ULID）
 * @param capturedAt 抓拍时刻（RFC3339）。使用本地时区，与详情页展示的时间戳
 *   保持同源，避免「页面上写 20:00、文件名写 12:00」。
 * @returns `20260331_200000-{id}.jpg`；`capturedAt` 无法解析时退化为
 *   `{id}.jpg`，绝不产出 `Invalid Date-{id}.jpg` 这种垃圾名。
 */
export function evidenceFileName(id: string, capturedAt: string): string {
  const stamp = captureStamp(capturedAt)
  return stamp ? `${stamp}-${id}.jpg` : `${id}.jpg`
}

function captureStamp(capturedAt: string): string {
  const at = new Date(capturedAt)
  return Number.isNaN(at.getTime()) ? '' : format(at, 'yyyyMMdd_HHmmss')
}
