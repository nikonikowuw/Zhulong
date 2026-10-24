import { useCallback, useMemo } from 'react'
import { useAllCameras } from '@/features/cameras/contract'
import { type CameraNameLookup } from '../data/camera-label'

/**
 * 感知记录页的摄像机名称解析器（`cameraId → name`）。
 *
 * 为什么走 `/cameras` 而不是给记录行冗余 `camera_name` 字段：
 *
 * - `camera_id` 外键是 `ON DELETE CASCADE`（000014_perception_foreign_keys），
 *   摄像机一删记录连同 `snapshots/{camera_id}/` 一起消失。所以不存在
 *   「父行没了、只剩快照可读」的状态 —— 而快照唯一的价值恰恰是那种状态
 *   （参见算法为什么用 `ON DELETE SET NULL` + `algorithm_name` 快照）。
 * - 快照语义是「拍照那一刻的名字」，用在摄像机上反而制造矛盾：运维把
 *   "cam-01" 重命名成 "南门枪机" 之后，历史记录仍显示 "cam-01"，
 *   会被当成 bug 反复报。而记录真正需要回答的是「这段证据来自哪台设备」，
 *   设备的**当前**标识才是用户心智里的答案。
 * - 代价是一次查询 + N 次内存查找，不是 N+1 次 SQL。
 *
 * 为什么不直接 join：`internal/record` 是 GORM 模型层，join 会把 camera
 * 表列塞进 record 模型，破坏「record 域不认识 camera 域」的边界。名称解析
 * 放在返回之后，归属留在表现层。
 *
 * 为什么用 `useAllCameras` 而不是 `useCameras({ pageSize: 100 })`：
 * 后端单页上限是 100，而 `MaxCameraCapacity` 为 128 —— 记录行可能指向
 * 第二页的摄像机，只取第一页会静默退化成显示 ID。
 *
 * 数据源与「摄像机管理」「实时预览」共用同一个 TanStack Query 缓存
 * （`CAMERAS_QUERY_KEY`），SSE 增量 rename 后记录页名称同步刷新，无需额外失效。
 * 进记录页时通常已经预热过，因此实际网络开销为 0。
 */
export function useCameraNames(): CameraNameLookup {
  const items = useAllCameras().data?.items

  // 索引成 Map：一页 100 条记录逐个 items.find() 在 128 台相机下是
  // 上万次比较，且每次渲染重算。建表一次即可。
  const byID = useMemo(() => {
    if (!items) return undefined
    return new Map(items.map((cam) => [cam.id, cam.name]))
  }, [items])

  return useCallback(
    (cameraId: string) => {
      if (!byID) return undefined
      return byID.get(cameraId)
    },
    [byID]
  )
}
