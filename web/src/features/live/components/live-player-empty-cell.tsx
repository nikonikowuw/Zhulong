import { Camera } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
    return 'border-primary bg-primary/10 shadow-[inset_0_0_0_1px_hsl(var(--primary))]'
  }
  if (isActive) {
    return 'border-primary bg-card/95 shadow-[inset_0_0_0_1px_hsl(var(--primary))]'
  }
  return 'border-border/60 bg-card/30 hover:border-border hover:bg-card/60'
}

function getIconContainerSize(isMicro: boolean, isCompact: boolean): string {
  if (isMicro) return 'h-6 w-6 rounded-md'
  if (isCompact) return 'h-8 w-8 rounded-lg'
  return 'h-10 w-10 rounded-xl'
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
      className={`group relative flex h-full w-full cursor-pointer flex-col items-center justify-center overflow-hidden rounded-xl border-2 transition-all select-none ${getEmptyCellBorderClass(
        isDragOver,
        isActive
      )}`}
    >
      {/* 右上角分屏编号角标 */}
      {!isMicro && (
        <div className='absolute top-2 right-2 sm:top-2.5 sm:right-2.5'>
          <Badge
            variant={isActive ? 'default' : 'outline'}
            className={`font-mono text-[10px] ${
              isActive
                ? 'bg-primary text-primary-foreground'
                : 'text-muted-foreground'
            }`}
          >
            #{cell.cellId + 1}
          </Badge>
        </div>
      )}

      {/* 中心占位引导图标与文字 */}
      <div
        className={`pointer-events-none flex flex-col items-center text-center ${
          isMicro ? 'gap-1 p-1' : 'gap-1.5 p-2 sm:p-3'
        }`}
      >
        <div
          className={`flex items-center justify-center rounded-lg border transition-all duration-200 group-hover:scale-105 ${getIconContainerSize(
            isMicro,
            isCompact
          )} ${
            isActive
              ? 'border-primary bg-primary/15 text-primary'
              : 'border-border/80 bg-muted/60 text-muted-foreground group-hover:border-primary/40 group-hover:bg-primary/10 group-hover:text-primary'
          }`}
        >
          <Camera className={getCameraIconSize(isMicro, isCompact)} />
        </div>
        <div>
          <p
            className={`font-semibold ${
              isActive ? 'text-primary' : 'text-foreground'
            } ${isMicro ? 'text-[11px]' : 'text-xs'}`}
          >
            {isDragOver
              ? t('releaseToPlay')
              : `#${cell.cellId + 1} · ${t('idle', { defaultValue: 'Idle' })}`}
          </p>
          {!isCompact && (
            <p className='mt-0.5 text-[11px] text-muted-foreground'>
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
