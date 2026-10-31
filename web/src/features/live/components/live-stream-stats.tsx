import { categoryBadgeClass, type CategoryHue } from '@/lib/category-palette'
import { type StreamStats } from '../types'

/** 解码后端 → §1.3.6 分类色板槽位（显式映射）。 */
const DECODER_HUE: Record<StreamStats['decoderMode'], CategoryHue> = {
  WebCodecs: 'blue',
  MSE: 'violet',
  WASM: 'cyan',
  Mock: 'emerald',
}

function getFpsColorClass(fps: number): string {
  if (fps >= 24) return 'text-emerald-500 font-medium'
  if (fps >= 15) return 'text-amber-500 font-medium'
  return 'text-rose-500 font-medium'
}

interface LiveStreamStatsProps {
  stats: StreamStats
  className?: string
  compact?: boolean
}

export function LiveStreamStats({
  stats,
  className = '',
  compact = false,
}: LiveStreamStatsProps): React.JSX.Element {
  return (
    <div
      className={`inline-flex items-center gap-1.5 rounded-md border border-white/15 bg-black/75 px-2 py-0.5 font-mono text-[10px] text-white/90 shadow-sm backdrop-blur-md transition-all select-none sm:text-[11px] ${className}`}
    >
      <span
        className={`py-0.2 rounded px-1.5 text-[9px] font-semibold tracking-wide uppercase sm:text-[10px] ${categoryBadgeClass(
          DECODER_HUE[stats.decoderMode]
        )}`}
      >
        {stats.decoderMode}
      </span>
      {!compact && stats.resolution && (
        <>
          <span className='text-white/70'>{stats.resolution}</span>
          <span className='text-white/20'>|</span>
        </>
      )}
      <span
        className={`inline-flex items-center gap-1 ${getFpsColorClass(stats.fps)}`}
      >
        <span
          className={`h-1.5 w-1.5 rounded-full ${
            stats.fps >= 24
              ? 'bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.8)]'
              : stats.fps >= 15
                ? 'bg-amber-400'
                : 'bg-rose-400'
          }`}
        />
        {stats.fps} FPS
      </span>
      {!compact && stats.bitrateKbps > 0 && (
        <>
          <span className='text-white/20'>|</span>
          <span className='text-white/60'>{stats.bitrateKbps} kbps</span>
        </>
      )}
    </div>
  )
}
