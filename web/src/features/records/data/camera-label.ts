/**
 * 感知记录的「摄像机」展示名解析。
 *
 * 记录行（recognitions / alarms / captures）只存 `camera_id`，不冗余摄像机名称：
 * 外键是 `ON DELETE CASCADE`（000014_perception_foreign_keys），摄像机一删记录连同
 * `snapshots/{camera_id}/` 下的证据文件一起消失，**不存在「记录还在但父行没了」的状态**。
 * 因此这里不需要像 `algorithmName` 那样依赖写入时快照，按当前配置解析即可。
 *
 * 与 `algorithmLabel` 的差别不是风格问题：算法是「工具」，卸载工具不该毁证据（SET NULL
 * + 名称快照）；摄像机是「证据的物理载体」，载体没了证据也无意义（CASCADE）。
 */
export type CameraNameLookup = (cameraId: string) => string | undefined

/**
 * 把记录的 `cameraId` 解析成可读名称。
 *
 * - 解析命中 → 名称
 * - 摄像机列表尚未加载 / 查无此相机 → 回退裸 ID，绝不渲染空白单元格
 */
export function cameraLabel(
  cameraId: string,
  lookup?: CameraNameLookup
): string {
  const name = lookup?.(cameraId)?.trim()
  return name || cameraId
}
