import { ChevronDown, Tv, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
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
      className={cn(
        'absolute inset-x-2 top-2 z-20 flex items-center justify-between rounded-lg border border-white/15 bg-black/70 text-white opacity-80 shadow-md backdrop-blur-md transition-all duration-200 select-none group-hover:opacity-100',
        isMicro ? 'px-1.5 py-0.5 text-[10px]' : 'px-2.5 py-1 text-xs'
      )}
    >
      {/* 左侧：在线指示点、摄像机名称、通道编号 */}
      <div className='flex items-center gap-1.5 font-medium'>
        <span className='relative flex h-2 w-2'>
          <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75' />
          <span className='relative inline-flex h-2 w-2 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.8)]' />
        </span>
        <span
          className={cn(
            'truncate font-medium text-white/95 drop-shadow-xs',
            isMicro ? 'max-w-[70px]' : 'max-w-[130px] sm:max-w-[180px]'
          )}
        >
          {cell.cameraName || t('cameraFallback', { id: cell.cameraId })}
        </span>
        <Badge
          variant='secondary'
          className='border border-white/10 bg-white/15 px-1 py-0 font-mono text-[9px] text-white/90'
        >
          #{cell.cellId + 1}
        </Badge>
      </div>

      {/* 右侧操作按钮组：hover 时高亮展开 */}
      <div className='flex items-center gap-0.5 transition-opacity duration-200'>
        {/* 主/子流切换 */}
        {cell.hasSubStream ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant='ghost'
                size='sm'
                className={cn(
                  'gap-0.5 text-white/80 hover:bg-white/15 hover:text-white',
                  isMicro ? 'h-5 px-1 text-[10px]' : 'h-6 px-1.5 text-[11px]'
                )}
                onClick={(e) => e.stopPropagation()}
              >
                <span>
                  {cell.streamType === 'main'
                    ? t('mainStream')
                    : t('subStream')}
                </span>
                <ChevronDown className='h-2.5 w-2.5 opacity-60' />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='min-w-[110px]'>
              <DropdownMenuItem onClick={() => onSetStreamType('main')}>
                {t('mainStreamHd')}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onSetStreamType('sub')}>
                {t('subStreamSd')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : (
          <span
            className={cn(
              'font-mono text-white/50 select-none',
              isMicro ? 'px-1 text-[9px]' : 'px-1.5 text-[10px]'
            )}
          >
            HD
          </span>
        )}

        {/* 彩条测试图切换 */}
        <Button
          variant='ghost'
          size='icon'
          title={t('toggleMockStream')}
          className={cn(
            isMicro ? 'h-5 w-5' : 'h-6 w-6',
            useMockCanvas
              ? 'bg-purple-500/25 text-purple-300 hover:bg-purple-500/35'
              : 'text-white/70 hover:bg-white/15 hover:text-white'
          )}
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
          className={cn(
            isMicro ? 'h-5 w-5' : 'h-6 w-6',
            'text-white/70 hover:bg-rose-500/25 hover:text-rose-300'
          )}
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
