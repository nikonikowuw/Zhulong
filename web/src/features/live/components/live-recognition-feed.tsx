import {
  Camera,
  Clock,
  Search,
  Trash2,
  UserCheck,
  UserX,
  X,
  Sparkles,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  useRecognitionRecords,
  type RecordStatusFilter,
} from '../hooks/use-recognition-records'

interface LiveRecognitionFeedProps {
  isOpen: boolean
  onClose: () => void
  cameraNames?: Record<string, string>
}

export function LiveRecognitionFeed({
  isOpen,
  onClose,
  cameraNames = {},
}: LiveRecognitionFeedProps): React.JSX.Element | null {
  const { t } = useTranslation('live')
  const {
    records,
    totalCount,
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    clearRecords,
  } = useRecognitionRecords()

  if (!isOpen) return null

  const formatTime = (ts: number) => {
    const d = new Date(ts)
    return d.toTimeString().split(' ')[0]
  }

  return (
    <div className='flex h-full w-80 animate-in flex-col rounded-xl border border-border/80 bg-card shadow-lg transition-all duration-200 slide-in-from-right'>
      {/* 顶部标题栏 */}
      <div className='flex items-center justify-between border-b border-border/60 p-3'>
        <div className='flex items-center gap-2'>
          <Sparkles className='h-4 w-4 text-primary' />
          <h3 className='text-sm font-semibold tracking-tight text-foreground'>
            {t('recognitionFeedTitle', '实时识别流')}
          </h3>
          <Badge variant='secondary' className='h-5 px-1.5 font-mono text-xs'>
            {totalCount}
          </Badge>
        </div>

        <div className='flex items-center gap-1'>
          <Button
            variant='ghost'
            size='icon'
            className='h-7 w-7 text-muted-foreground hover:text-destructive'
            onClick={clearRecords}
            title={t('clearRecords', '清空记录')}
          >
            <Trash2 className='h-3.5 w-3.5' />
          </Button>
          <Button
            variant='ghost'
            size='icon'
            className='h-7 w-7 text-muted-foreground hover:text-foreground'
            onClick={onClose}
          >
            <X className='h-4 w-4' />
          </Button>
        </div>
      </div>

      {/* 搜索与过滤栏 */}
      <div className='flex flex-col gap-2 border-b border-border/40 p-2.5'>
        <div className='relative'>
          <Search className='absolute start-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground' />
          <Input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('searchPersonPlaceholder', '搜索姓名或人员 ID...')}
            className='h-8 ps-8 text-xs'
          />
        </div>

        <div className='grid grid-cols-3 gap-1 rounded-lg bg-muted/60 p-0.5 text-center text-xs font-medium'>
          {(['all', 'matched', 'unmatched'] as RecordStatusFilter[]).map(
            (tab) => {
              const labelMap: Record<RecordStatusFilter, string> = {
                all: t('filterAll', '全部'),
                matched: t('filterMatched', '已识别'),
                unmatched: t('filterUnmatched', '未登记'),
              }
              const isActive = statusFilter === tab
              return (
                <button
                  key={tab}
                  type='button'
                  onClick={() => setStatusFilter(tab)}
                  className={`rounded-md py-1 transition-all ${
                    isActive
                      ? 'bg-background text-foreground shadow-xs'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {labelMap[tab]}
                </button>
              )
            }
          )}
        </div>
      </div>

      {/* 识别记录滚动列表 */}
      <ScrollArea className='flex-1 p-2.5'>
        {records.length === 0 ? (
          <div className='flex h-48 flex-col items-center justify-center p-4 text-center text-muted-foreground'>
            <div className='mb-2 rounded-full bg-muted p-2.5'>
              <UserCheck className='h-5 w-5 opacity-40' />
            </div>
            <p className='text-xs font-medium'>
              {t('noRecords', '暂无识别记录')}
            </p>
            <p className='mt-1 text-[11px] text-muted-foreground/80'>
              {t('noRecordsDesc', '视频检测到目标人脸时将实时在此展示')}
            </p>
          </div>
        ) : (
          <div className='flex flex-col gap-2'>
            {records.map((item) => {
              const simPct = Math.round(item.similarity * 100)
              const cameraTitle = cameraNames[item.cameraId] || item.cameraId

              return (
                <div
                  key={item.id}
                  className={cn(
                    'flex items-start gap-2.5 rounded-lg border p-2.5 transition-colors',
                    statusLevelClass(item.matched ? 'active' : 'warning')
                  )}
                >
                  {/* 头像/图标 */}
                  <div
                    className={cn(
                      'mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full',
                      item.matched
                        ? statusTextClass('active')
                        : statusTextClass('warning')
                    )}
                  >
                    {item.matched ? (
                      <UserCheck className='h-4 w-4' />
                    ) : (
                      <UserX className='h-4 w-4' />
                    )}
                  </div>

                  {/* 核心详情 */}
                  <div className='flex min-w-0 flex-1 flex-col gap-1'>
                    <div className='flex items-center justify-between gap-1'>
                      <span className='truncate text-xs font-semibold text-foreground'>
                        {item.matched
                          ? item.personName || item.personId
                          : t('unregisteredPerson', '未登记目标')}
                      </span>
                      <span className='flex shrink-0 items-center gap-1 font-mono text-[10px] text-muted-foreground'>
                        <Clock className='h-2.5 w-2.5' />
                        {formatTime(item.timestamp)}
                      </span>
                    </div>

                    <div className='flex items-center justify-between gap-1 text-[11px]'>
                      <span
                        className='flex items-center gap-1 truncate text-muted-foreground'
                        title={cameraTitle}
                      >
                        <Camera className='h-2.5 w-2.5 shrink-0' />
                        <span className='truncate'>{cameraTitle}</span>
                      </span>

                      <Badge
                        variant='outline'
                        className={`h-4 shrink-0 px-1 py-0 font-mono text-[10px] ${
                          item.matched
                            ? 'border-emerald-500/30 text-emerald-600 dark:text-emerald-400'
                            : 'border-amber-500/30 text-amber-600 dark:text-amber-400'
                        }`}
                      >
                        {simPct}%
                      </Badge>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </ScrollArea>
    </div>
  )
}
