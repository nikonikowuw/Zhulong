import { type TrajectoryNodeInfo } from '../../data/schema'

interface TrajectoryLayerProps {
  nodes: TrajectoryNodeInfo[]
  /** 当前高亮的节点下标；同一下标对应的线段同时高亮。 */
  activeNodeIndex?: number
  /** 底图固有宽度（像素），用于把归一化坐标换算成 SVG 用户单位。 */
  width: number
  /** 底图固有高度（像素）。 */
  height: number
  /** 播放中时沿高亮线段移动的光标周期（毫秒）。 */
  segmentDurationMs?: number
  /** 播放中为 true；暂停时保留静态连线，不跑光标动画。 */
  isPlaying?: boolean
}

/**
 * 一组相邻节点构成的线段；`next` 一定存在（最后一节点不构成线段）。
 */
interface TrajectorySegment {
  node: TrajectoryNodeInfo
  next: TrajectoryNodeInfo
}

function buildSegments(nodes: TrajectoryNodeInfo[]): TrajectorySegment[] {
  return nodes.slice(0, -1).map((node, index) => ({
    node,
    next: nodes[index + 1],
  }))
}

/**
 * SVG 轨迹连线层。
 *
 * 坐标使用底图固有像素而不是百分比：`<animateMotion path>` 与 marker 的箭头
 * 缩放都需要用户单位，百分比 d 属性在 SVG 里根本不合法。
 *
 * 颜色一律走语义 token（`stroke-primary` / `stroke-muted-foreground`）：
 * amber/teal 在本仓是状态语义的专属色相，不能用来说明「这一段是当前段」。
 */
export function TrajectoryLayer({
  nodes,
  activeNodeIndex,
  width,
  height,
  segmentDurationMs = 2000,
  isPlaying = false,
}: TrajectoryLayerProps) {
  if (nodes.length < 2) {
    return null
  }

  const toX = (x: number) => x * width
  const toY = (y: number) => y * height

  const segments = buildSegments(nodes)
  const activeSegment =
    activeNodeIndex !== undefined ? segments[activeNodeIndex] : undefined

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio='none'
      className='pointer-events-none absolute inset-0 z-10 h-full w-full overflow-visible'
      aria-hidden='true'
    >
      <defs>
        <marker
          id='trajectory-arrow'
          viewBox='0 0 12 12'
          refX='9'
          refY='6'
          markerWidth='8'
          markerHeight='8'
          orient='auto-start-reverse'
          className='text-muted-foreground'
        >
          <path d='M 1 2 L 10 6 L 1 10 z' fill='currentColor' />
        </marker>
        <marker
          id='trajectory-arrow-active'
          viewBox='0 0 12 12'
          refX='9'
          refY='6'
          markerWidth='8'
          markerHeight='8'
          orient='auto-start-reverse'
          className='text-primary'
        >
          <path d='M 1 2 L 10 6 L 1 10 z' fill='currentColor' />
        </marker>
        <style>{`
          @keyframes trajectoryFlow {
            from { stroke-dashoffset: 28; }
            to { stroke-dashoffset: 0; }
          }
          .animate-trajectory-flow {
            animation: trajectoryFlow 1s linear infinite;
          }
        `}</style>
      </defs>

      {segments.map(({ node, next }, index) => {
        const isActiveSegment = index === activeNodeIndex

        const x1 = toX(node.x)
        const y1 = toY(node.y)
        const x2 = toX(next.x)
        const y2 = toY(next.y)

        return (
          <g key={`traj-segment-${node.seq}-${next.seq}`}>
            <line
              x1={x1}
              y1={y1}
              x2={x2}
              y2={y2}
              className='stroke-foreground/25'
              strokeWidth='5'
              strokeLinecap='round'
            />

            <line
              x1={x1}
              y1={y1}
              x2={x2}
              y2={y2}
              className={
                isActiveSegment
                  ? 'animate-trajectory-flow stroke-primary'
                  : 'animate-trajectory-flow stroke-muted-foreground/70'
              }
              strokeWidth={isActiveSegment ? '4' : '3'}
              strokeDasharray='8 6'
              strokeLinecap='round'
              markerEnd={
                isActiveSegment
                  ? 'url(#trajectory-arrow-active)'
                  : 'url(#trajectory-arrow)'
              }
            />
          </g>
        )
      })}

      {/*
        播放光标：用 SVG 动画沿高亮线段平滑移动，不经过 React 状态。
        逐帧 setState 会让整棵画布（底图 + 全部点位）跟着重渲染。
      */}
      {isPlaying && activeSegment && (
        <circle
          r={Math.max(4, Math.min(width, height) * 0.008)}
          className='fill-primary'
        >
          <animateMotion
            dur={`${segmentDurationMs}ms`}
            repeatCount='indefinite'
            path={`M ${toX(activeSegment.node.x)} ${toY(activeSegment.node.y)} L ${toX(activeSegment.next.x)} ${toY(activeSegment.next.y)}`}
          />
        </circle>
      )}
    </svg>
  )
}
