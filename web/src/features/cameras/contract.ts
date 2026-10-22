/**
 * 摄像机特性的跨特性契约入口。
 *
 * `features/cameras/index.tsx` 是路由页面入口，只应导出组件——在其中 re-export
 * hook 会触发 `react-refresh/only-export-components`（组件文件混入非组件导出会
 * 破坏 HMR 边界）。因此跨特性消费方统一从这里取相机契约：
 *
 * - `Camera` / `CameraListResponse` / `Stream`：`/cameras` 响应的唯一前端模型
 * - `useAllCameras`：全量列表（内部按后端分页上限拉全）
 * - `useCameraEvents`：`/api/v1/cameras/events` 的 SSE 增量通道
 *
 * 实时预览页（`features/live`）复用以上三者，不再自行建模 `/cameras` 响应，
 * 也不再用 `refetchInterval` 轮询在线状态。
 */
export { useAllCameras } from './hooks/use-cameras'
export { useCameraEvents } from './hooks/use-camera-events'
export {
  type Camera,
  type CameraListResponse,
  type Stream,
} from './data/schema'
