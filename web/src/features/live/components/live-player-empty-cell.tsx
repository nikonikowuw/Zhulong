import { Camera, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type LiveCellState } from '../types'

interface LivePlayerEmptyCellProps {
  cell: LiveCellState
  isActive: boolean
  isDragOver: boolean
  isMicro: boolean
  isCompact: boolean
  onSelect: () => void
  onDragOver: (e: React.DragEvent) => void
  onDragLeave: () => void
  onDrop: (e: React.DragEvent) => void
}

function getEmptyCellBorderClass(
  isDragOver: boolean,
  isActive: boolean
): string {
  if (isDragOver) {
    return 'border-primary bg-primary/10 shadow-[0_0_20px_rgba(var(--primary),0.2)] ring-2 ring-primary/30 scale-[0.995]'
  }
  if (isActive) {
    return 'border-primary/90 bg-card/85 shadow-[0_0_16px_rgba(var(--primary),0.12)] ring-1 ring-primary/40'
  }
  return 'border-border/50 bg-card/25 hover:border-border/90 hover:bg-card/50'
}

function getIconContainerSize(isMicro: boolean, isCompact: boolean): string {
  if (isMicro) return 'h-7 w-7 rounded-md'
  if (isCompact) return 'h-9 w-9 rounded-lg'
  return 'h-11 w-11 rounded-xl'
}

function getCameraIconSize(isMicro: boolean, isCompact: boolean): string {
  if (isMicro) return 'h-3.5 w-3.5'
  if (isCompact) return 'h-4 w-4'
  return 'h-5 w-5'
}

export function LivePlayerEmptyCell({
  cell,
  isActive,
  isDragOver,
  isMicro,
  isCompact,
  onSelect,
  onDragOver,
  onDragLeave,
  onDrop,
}: LivePlayerEmptyCellProps): React.JSX.Element {
  const { t } = useTranslation('live')

  return (
    <div
      onClick={onSelect}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      className={cn(
        'group relative flex h-full w-full cursor-pointer flex-col items-center justify-center overflow-hidden rounded-xl border transition-all duration-200 select-none',
        getEmptyCellBorderClass(isDragOver, isActive)
      )}
    >
      {/* 战术四角标尺准星 (Corner Reticles) */}
      <span
        aria-hidden='true'
        className={cn(
          'pointer-events-none absolute top-1.5 left-1.5 h-2 w-2 border-s border-t transition-colors duration-200',
          isActive
            ? 'border-primary shadow-[0_0_6px_rgba(var(--primary),0.5)]'
            : isDragOver
              ? 'border-primary'
              : 'border-muted-foreground/30 group-hover:border-primary/50'
        )}
      />
      <span
        aria-hidden='true'
        className={cn(
          'pointer-events-none absolute top-1.5 right-1.5 h-2 w-2 border-e border-t transition-colors duration-200',
          isActive
            ? 'border-primary shadow-[0_0_6px_rgba(var(--primary),0.5)]'
            : isDragOver
              ? 'border-primary'
              : 'border-muted-foreground/30 group-hover:border-primary/50'
        )}
      />
      <span
        aria-hidden='true'
        className={cn(
          'pointer-events-none absolute bottom-1.5 left-1.5 h-2 w-2 border-s border-b transition-colors duration-200',
          isActive
            ? 'border-primary shadow-[0_0_6px_rgba(var(--primary),0.5)]'
            : isDragOver
              ? 'border-primary'
              : 'border-muted-foreground/30 group-hover:border-primary/50'
        )}
      />
      <span
        aria-hidden='true'
        className={cn(
          'pointer-events-none absolute right-1.5 bottom-1.5 h-2 w-2 border-e border-b transition-colors duration-200',
          isActive
            ? 'border-primary shadow-[0_0_6px_rgba(var(--primary),0.5)]'
            : isDragOver
              ? 'border-primary'
              : 'border-muted-foreground/30 group-hover:border-primary/50'
        )}
      />

      {/* 微弱中央径向微光与网格背景 */}
      <div
        aria-hidden='true'
        className={cn(
          'pointer-events-none absolute inset-0 transition-opacity duration-300',
          isActive
            ? 'bg-[radial-gradient(ellipse_at_center,rgba(var(--primary),0.08)_0%,transparent_70%)] opacity-100'
            : 'opacity-0 group-hover:bg-[radial-gradient(ellipse_at_center,rgba(var(--primary),0.05)_0%,transparent_70%)] group-hover:opacity-40'
        )}
      />

      {/* 右上角分屏编号角标 */}
      {!isMicro && (
        <div className='absolute top-2 right-2 z-10 sm:top-2.5 sm:right-2.5'>
          <Badge
            variant={isActive ? 'default' : 'outline'}
            className={cn(
              'px-1.5 py-0 font-mono text-[10px] transition-colors',
              isActive
                ? 'bg-primary text-primary-foreground shadow-xs'
                : 'border-border/60 bg-background/50 text-muted-foreground backdrop-blur-xs'
            )}
          >
            #{cell.cellId + 1}
          </Badge>
        </div>
      )}

      {/* 中心占位引导图标与文字 */}
      <div
        className={cn(
          'pointer-events-none relative z-10 flex flex-col items-center text-center transition-transform duration-200',
          isMicro ? 'gap-1 p-1' : 'gap-2 p-2 sm:p-3',
          isDragOver ? 'scale-105' : 'group-hover:scale-[1.02]'
        )}
      >
        <div
          className={cn(
            'relative flex items-center justify-center border shadow-xs transition-all duration-200',
            getIconContainerSize(isMicro, isCompact),
            isActive
              ? 'border-primary/70 bg-primary/15 text-primary shadow-[0_0_12px_rgba(var(--primary),0.2)]'
              : isDragOver
                ? 'animate-pulse border-primary bg-primary/25 text-primary'
                : 'border-border/80 bg-muted/40 text-muted-foreground/80 group-hover:border-primary/50 group-hover:bg-primary/10 group-hover:text-primary'
          )}
        >
          <Camera className={getCameraIconSize(isMicro, isCompact)} />
          {/* 小加号指示器 */}
          {!isMicro && (
            <span
              className={cn(
                'absolute -right-1 -bottom-1 flex h-3.5 w-3.5 items-center justify-center rounded-full border border-background text-[9px] font-bold shadow-xs',
                isActive
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground group-hover:bg-primary group-hover:text-primary-foreground'
              )}
            >
              <Plus className='h-2.5 w-2.5' />
            </span>
          )}
        </div>
        <div>
          <p
            className={cn(
              'font-semibold transition-colors',
              isActive ? 'text-primary' : 'text-foreground/90',
              isMicro ? 'text-[11px]' : 'text-xs'
            )}
          >
            {isDragOver
              ? t('releaseToPlay')
              : `#${cell.cellId + 1} · ${t('idle', { defaultValue: 'Idle' })}`}
          </p>
          {!isCompact && (
            <p className='mt-0.5 text-[11px] leading-tight text-muted-foreground/80'>
              {t('dragCameraToPlay', {
                defaultValue: '拖拽左侧设备到此窗口播放',
              })}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
