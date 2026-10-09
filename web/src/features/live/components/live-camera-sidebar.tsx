import { useState } from 'react'
import {
  Search,
  Video,
  ChevronLeft,
  ChevronRight,
  Play,
  VideoOff,
  RotateCcw,
  Loader2,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { type LiveCameraItem, type StreamType } from '../types'

interface LiveCameraSidebarProps {
  cameras: LiveCameraItem[]
  isLoading?: boolean
  error?: Error | null
  onRetry?: () => void
  activeCellId: number
  onAssignCamera: (camera: LiveCameraItem, streamType?: StreamType) => void
  isCollapsed: boolean
  onToggleCollapse: () => void
}

export function LiveCameraSidebar({
  cameras,
  isLoading = false,
  error = null,
  onRetry,
  activeCellId,
  onAssignCamera,
  isCollapsed,
  onToggleCollapse,
}: LiveCameraSidebarProps): React.JSX.Element {
  const { t } = useTranslation('live')
  const [search, setSearch] = useState('')

  const normalizedSearch = search.trim().toLowerCase()
  const filteredCameras = normalizedSearch
    ? cameras.filter(
        (cam) =>
          cam.name.toLowerCase().includes(normalizedSearch) ||
          cam.ip.includes(normalizedSearch)
      )
    : cameras

  function renderSidebarContent(): React.JSX.Element | React.JSX.Element[] {
    // 1. 加载骨架屏
    if (isLoading) {
      return (
        <div className='space-y-2 py-2'>
          <div className='flex items-center justify-center gap-1.5 py-2 text-xs text-muted-foreground'>
            <Loader2 className='h-3.5 w-3.5 animate-spin' />
            <span>{t('loadingCameras')}</span>
          </div>
          <Skeleton className='h-14 w-full rounded-lg' />
          <Skeleton className='h-14 w-full rounded-lg' />
          <Skeleton className='h-14 w-full rounded-lg' />
        </div>
      )
    }

    // 2. 接口异常错误态
    if (error) {
      return (
        <div className='flex flex-col items-center justify-center py-8 text-center'>
          <div className='rounded-full bg-destructive/10 p-2.5 text-destructive'>
            <RotateCcw className='h-4 w-4' />
          </div>
          <p className='mt-2 text-xs font-medium text-destructive'>
            {t('fetchFailed')}
          </p>
          <p className='mt-1 max-w-[180px] text-[11px] text-muted-foreground'>
            {error.message || t('networkError')}
          </p>
          {onRetry && (
            <Button
              variant='outline'
              size='sm'
              onClick={onRetry}
              className='mt-3 h-7 text-xs'
            >
              {t('retry')}
            </Button>
          )}
        </div>
      )
    }

    // 3. 真实资产空状态 (Initial Empty State)
    if (cameras.length === 0) {
      return (
        <div className='flex flex-col items-center justify-center py-10 text-center'>
          <div className='rounded-full bg-muted p-3 text-muted-foreground'>
            <VideoOff className='h-5 w-5' />
          </div>
          <p className='mt-2.5 text-xs font-medium text-foreground'>
            {t('emptyCameras')}
          </p>
          <p className='mt-1 max-w-[180px] text-[11px] leading-relaxed text-muted-foreground'>
            {t('emptyCamerasDesc')}
          </p>
        </div>
      )
    }

    // 4. 筛选无匹配空状态 (Filter Empty State)
    if (filteredCameras.length === 0) {
      return (
        <div className='flex flex-col items-center justify-center py-8 text-center'>
          <p className='text-xs text-muted-foreground'>
            {t('noMatchingCameras')}
          </p>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => setSearch('')}
            className='mt-2 h-7 text-xs text-primary'
          >
            {t('clearSearch')}
          </Button>
        </div>
      )
    }

    // 5. 真实摄像机列表
    return filteredCameras.map((camera) => (
      <div
        key={camera.id}
        draggable
        onDragStart={(e) => {
          e.dataTransfer.setData('application/json', JSON.stringify(camera))
          e.dataTransfer.effectAllowed = 'copy'
        }}
        onDoubleClick={() => onAssignCamera(camera, 'main')}
        className='group flex cursor-grab flex-col gap-1.5 rounded-lg border border-border/50 bg-card p-2.5 text-xs shadow-2xs transition-all hover:border-primary/50 hover:bg-accent/40 active:cursor-grabbing'
      >
        <div className='flex items-center justify-between'>
          <div className='flex max-w-[150px] items-center gap-1.5 truncate font-medium'>
            <span
              className={`h-2 w-2 rounded-full ${
                camera.status === 'online'
                  ? 'bg-emerald-500 shadow-xs shadow-emerald-500/50'
                  : 'bg-destructive shadow-xs shadow-destructive/50'
              }`}
            />
            <span className='truncate'>{camera.name}</span>
          </div>

          <Button
            variant='ghost'
            size='icon'
            className='h-6 w-6 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 hover:bg-primary/10 hover:text-primary'
            title={t('dragOrClickTip', { id: activeCellId + 1 })}
            onClick={() => onAssignCamera(camera, 'main')}
          >
            <Play className='h-3 w-3 fill-current' />
          </Button>
        </div>

        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='font-mono'>{camera.ip}</span>
          <div className='flex gap-1'>
            <Badge
              variant='outline'
              className='border-border/60 px-1 py-0 text-[10px] font-normal'
            >
              {camera.codec.toUpperCase()}
            </Badge>
            {camera.hasSubStream && (
              <Badge
                variant='secondary'
                className='px-1 py-0 text-[10px] font-normal'
              >
                {t('dualStream')}
              </Badge>
            )}
          </div>
        </div>
      </div>
    ))
  }

  if (isCollapsed) {
    return (
      <TooltipProvider delayDuration={200}>
        <div className='flex h-full w-12 flex-col items-center justify-between rounded-xl border border-border bg-card py-3 shadow-xs'>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='ghost'
                size='icon'
                className='h-8 w-8 text-muted-foreground hover:text-foreground'
                onClick={onToggleCollapse}
              >
                <ChevronRight className='h-4 w-4' />
              </Button>
            </TooltipTrigger>
            <TooltipContent side='right'>{t('expandSidebar')}</TooltipContent>
          </Tooltip>

          <div className='flex flex-col items-center gap-2'>
            <div className='rounded-full bg-muted p-2 text-muted-foreground'>
              <Video className='h-4 w-4' />
            </div>
            <Badge variant='secondary' className='h-5 px-1.5 text-[10px]'>
              {cameras.length}
            </Badge>
          </div>

          <div className='h-4' />
        </div>
      </TooltipProvider>
    )
  }

  return (
    <div className='flex h-full w-64 flex-col rounded-xl border border-border bg-card shadow-xs sm:w-72'>
      {/* 侧栏顶栏 */}
      <div className='flex items-center justify-between border-b border-border/80 px-3.5 py-3'>
        <div className='flex items-center gap-2 text-sm font-semibold tracking-tight'>
          <Video className='h-4 w-4 text-primary' />
          <span>{t('sidebarTitle')}</span>
          <Badge variant='secondary' className='font-mono text-xs'>
            {cameras.length}
          </Badge>
        </div>
        <Button
          variant='ghost'
          size='icon'
          title={t('collapseSidebar')}
          className='h-7 w-7 text-muted-foreground hover:text-foreground'
          onClick={onToggleCollapse}
        >
          <ChevronLeft className='h-4 w-4' />
        </Button>
      </div>

      {/* 搜索框 */}
      <div className='p-3 pb-2'>
        <div className='relative'>
          <Search className='absolute top-2.5 left-2.5 h-3.5 w-3.5 text-muted-foreground' />
          <Input
            placeholder={t('searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className='h-8 pr-7 pl-8 text-xs'
          />
          {search && (
            <Button
              variant='ghost'
              size='icon'
              onClick={() => setSearch('')}
              className='absolute top-1.5 right-1 h-5 w-5 text-muted-foreground hover:text-foreground'
              title={t('clearSearch')}
            >
              <X className='h-3 w-3' />
            </Button>
          )}
        </div>
      </div>

      {/* 提示文案 */}
      <div className='px-3.5 py-1 text-[11px] text-muted-foreground'>
        {t('dragOrClickTip', { id: activeCellId + 1 })}
      </div>

      {/* 摄像机列表或状态展示 */}
      <ScrollArea className='flex-1 px-3 py-1.5'>
        <div className='space-y-1.5 pb-3'>{renderSidebarContent()}</div>
      </ScrollArea>
    </div>
  )
}
