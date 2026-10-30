/**
 * 实时监看特性的跨特性契约入口。
 *
 * `features/live/index.tsx` 是路由页面入口，只导出页面组件；把预览画布这类
 * 非页面产物从这里暴露，消费方（电子地图点位验真）就不必穿透导入
 * `features/live/components/*`。
 */
export {
  CameraFrameSurface,
  type CameraFrameSurfaceProps,
  type FrameSurfaceState,
  type ImageRect,
} from './components/camera-frame-surface'
