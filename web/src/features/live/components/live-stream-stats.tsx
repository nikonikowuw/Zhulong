import { type StreamStats } from '../types'

const DECODER_BADGE_CLASSES: Record<StreamStats['decoderMode'], string> = {
  WebCodecs:
    'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/30',
  MSE: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30',
  WASM: 'bg-sky-500/15 text-sky-600 dark:text-sky-400 border-sky-500/30',
  Mock: 'bg-purple-500/15 text-purple-600 dark:text-purple-400 border-purple-500/30',
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
        className={`rounded border px-1 py-0 text-[10px] font-semibold ${
          DECODER_BADGE_CLASSES[stats.decoderMode]
        }`}
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
