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
      className={`inline-flex items-center gap-1.5 rounded-md border border-border/60 bg-background/85 px-1.5 py-0.5 font-mono text-[11px] text-foreground shadow-xs backdrop-blur-md ${className}`}
    >
      <span
        className={`rounded border px-1 py-0 text-[10px] font-semibold ${categoryBadgeClass(
          DECODER_HUE[stats.decoderMode]
        )}`}
      >
        {stats.decoderMode}
      </span>
      {!compact && (
        <>
          <span className='text-muted-foreground'>{stats.resolution}</span>
          <span className='text-border'>|</span>
        </>
      )}
      <span className={getFpsColorClass(stats.fps)}>{stats.fps} FPS</span>
      {!compact && stats.bitrateKbps > 0 && (
        <>
          <span className='text-border'>|</span>
          <span className='text-muted-foreground'>
            {stats.bitrateKbps} kbps
          </span>
        </>
      )}
    </div>
  )
}
