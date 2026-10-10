import { ChevronDown, Tv, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type LiveCellState, type StreamType } from '../types'

interface LivePlayerCellHeaderProps {
  cell: LiveCellState
  isMicro: boolean
  useMockCanvas: boolean
  onToggleMock: () => void
  onSetStreamType: (streamType: StreamType) => void
  onClear: () => void
}

export function LivePlayerCellHeader({
  cell,
  isMicro,
  useMockCanvas,
  onToggleMock,
  onSetStreamType,
  onClear,
}: LivePlayerCellHeaderProps): React.JSX.Element {
  const { t } = useTranslation('live')

  return (
    <div
      className={`absolute inset-x-2 top-2 z-20 flex items-center justify-between rounded-lg border border-border/40 bg-background/85 text-foreground opacity-0 shadow-xs backdrop-blur-md transition-all duration-200 group-hover:opacity-100 ${
        isMicro ? 'px-1.5 py-0.5 text-[10px]' : 'px-2.5 py-1.5 text-xs'
      }`}
    >
      <div className='flex items-center gap-1 font-medium'>
        <span className='h-2 w-2 animate-pulse rounded-full bg-emerald-500' />
        <span
          className={`truncate ${
            isMicro ? 'max-w-[70px]' : 'max-w-[130px] sm:max-w-[180px]'
          }`}
        >
          {cell.cameraName || t('cameraFallback', { id: cell.cameraId })}
        </span>
        <Badge variant='secondary' className='px-1 py-0 font-mono text-[9px]'>
          #{cell.cellId + 1}
        </Badge>
      </div>

      <div className='flex items-center gap-0.5'>
        {/* 主/子流切换 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant='ghost'
              size='sm'
              className={`gap-0.5 text-muted-foreground hover:text-foreground ${
                isMicro ? 'h-5 px-1 text-[10px]' : 'h-6 px-1.5 text-[11px]'
              }`}
              onClick={(e) => e.stopPropagation()}
            >
              <span>
                {cell.streamType === 'main' ? t('mainStream') : t('subStream')}
              </span>
              <ChevronDown className='h-2.5 w-2.5 opacity-50' />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align='end' className='min-w-[100px]'>
            <DropdownMenuItem onClick={() => onSetStreamType('main')}>
              {t('mainStreamHd')}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onSetStreamType('sub')}>
              {t('subStreamSd')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        {/* 彩条测试图切换 */}
        <Button
          variant='ghost'
          size='icon'
          title={t('toggleMockStream')}
          className={`${isMicro ? 'h-5 w-5' : 'h-6 w-6'} ${
            useMockCanvas
              ? 'text-purple-500'
              : 'text-muted-foreground hover:text-foreground'
          }`}
          onClick={(e) => {
            e.stopPropagation()
            onToggleMock()
          }}
        >
          <Tv className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
        </Button>

        {/* 关闭当前视频 */}
        <Button
          variant='ghost'
          size='icon'
          title={t('closeStream')}
          className={`${
            isMicro ? 'h-5 w-5' : 'h-6 w-6'
          } text-muted-foreground hover:bg-destructive/10 hover:text-destructive`}
          onClick={(e) => {
            e.stopPropagation()
            onClear()
          }}
        >
          <X className={isMicro ? 'h-3 w-3' : 'h-3.5 w-3.5'} />
        </Button>
      </div>
    </div>
  )
}
