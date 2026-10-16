import { useMemo } from 'react'
import { Cross2Icon } from '@radix-ui/react-icons'
import { LayoutGrid, RotateCcw, Table as TableIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useCameras } from '@/features/cameras/hooks/use-cameras'
import { cn } from '@/lib/utils'
import { type RecordsDateRange, type TimePresetKey } from '../data/time-presets'
import { useAlarmTypes } from '../hooks/use-alarm-types'
import { useCaptureTargetTypes } from '../hooks/use-capture-target-types'
import { RecordsDateRangePicker } from './records-date-range-picker'

export interface RecordsToolbarProps {
  tab: 'recognitions' | 'alarms' | 'captures'
  view: 'grid' | 'table'
  cameraId?: string
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  matched?: 'all' | 'true' | 'false'
  personId?: string
  alarmType?: string
  targetType?: string
  onTabChange: (tab: 'recognitions' | 'alarms' | 'captures') => void
  onViewChange: (view: 'grid' | 'table') => void
  onCameraChange: (cameraId?: string) => void
  onDateRangeChange: (range: RecordsDateRange) => void
  onMatchedChange?: (matched?: 'all' | 'true' | 'false') => void
  onPersonIdChange?: (personId?: string) => void
  onAlarmTypeChange?: (alarmType?: string) => void
  onTargetTypeChange?: (targetType?: string) => void
  onResetFilters: () => void
  onRefresh?: () => void
  isFetching?: boolean
}

export function RecordsToolbar({
  tab,
  view,
  cameraId,
  preset,
  startTime,
  endTime,
  matched,
  personId,
  alarmType,
  targetType,
  onViewChange,
  onCameraChange,
  onDateRangeChange,
  onMatchedChange,
  onPersonIdChange,
  onAlarmTypeChange,
  onTargetTypeChange,
  onResetFilters,
  onRefresh,
  isFetching = false,
}: RecordsToolbarProps) {
  const { t } = useTranslation('records')
  const { data: cameraData } = useCameras({ page: 1, pageSize: 100 })
  const cameras = cameraData?.items ?? []

  const { data: rawAlarmTypes = [] } = useAlarmTypes()
  const { data: rawTargetTypes = [] } = useCaptureTargetTypes()

  const alarmTypes = useMemo(() => {
    if (alarmType && !rawAlarmTypes.includes(alarmType)) {
      return [alarmType, ...rawAlarmTypes]
    }
    return rawAlarmTypes
  }, [alarmType, rawAlarmTypes])

  const targetTypes = useMemo(() => {
    if (targetType && !rawTargetTypes.includes(targetType)) {
      return [targetType, ...rawTargetTypes]
    }
    return rawTargetTypes
  }, [targetType, rawTargetTypes])

  const isFiltered = Boolean(
    cameraId ||
      preset ||
      startTime ||
      endTime ||
      (matched && matched !== 'all') ||
      personId ||
      alarmType ||
      targetType
  )

  return (
    <div className='flex items-center justify-between gap-2'>
      {/* 左侧：搜索/类型选择在最首位，紧接着是各个过滤控件 */}
      <div className='flex flex-1 flex-wrap items-center gap-2'>
        {/* 1. 首位过滤控件：识别记录使用人员搜索框，告警和抓拍使用动态类型下拉选择 */}
        {tab === 'recognitions' && (
          <Input
            placeholder={t('filters.personIdPlaceholder')}
            value={personId ?? ''}
            onChange={(e) =>
              onPersonIdChange?.(e.target.value.trim() || undefined)
            }
            className='h-8 w-37.5 lg:w-62.5 text-xs'
          />
        )}

        {tab === 'alarms' && (
          <Select
            value={alarmType ?? 'all'}
            onValueChange={(val) =>
              onAlarmTypeChange?.(val === 'all' ? undefined : val)
            }
          >
            <SelectTrigger className='h-8 w-[160px] text-xs'>
              <SelectValue placeholder={t('filters.alarmTypePlaceholder')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filters.allAlarmTypes')}</SelectItem>
              {alarmTypes.map((type) => (
                <SelectItem key={type} value={type}>
                  {type}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}

        {tab === 'captures' && (
          <Select
            value={targetType ?? 'all'}
            onValueChange={(val) =>
              onTargetTypeChange?.(val === 'all' ? undefined : val)
            }
          >
            <SelectTrigger className='h-8 w-[160px] text-xs'>
              <SelectValue placeholder={t('filters.targetTypePlaceholder')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filters.allTargetTypes')}</SelectItem>
              {targetTypes.map((type) => (
                <SelectItem key={type} value={type}>
                  {type}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}

        {/* 2. 摄像机筛选下拉 */}
        <Select
          value={cameraId ?? 'all'}
          onValueChange={(val) =>
            onCameraChange(val === 'all' ? undefined : val)
          }
        >
          <SelectTrigger className='h-8 w-[160px] text-xs'>
            <SelectValue placeholder={t('filters.cameraPlaceholder')} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='all'>{t('filters.allCameras')}</SelectItem>
            {cameras.map((cam) => (
              <SelectItem key={cam.id} value={cam.id}>
                {cam.name || cam.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {/* 3. 时间范围选择器 */}
        <RecordsDateRangePicker
          preset={preset}
          startTime={startTime}
          endTime={endTime}
          onRangeChange={onDateRangeChange}
        />

        {/* 4. Tab 专属细化过滤项 */}
        {tab === 'recognitions' && (
          <Select
            value={matched ?? 'all'}
            onValueChange={(val) =>
              onMatchedChange?.(
                val === 'all' ? undefined : (val as 'true' | 'false')
              )
            }
          >
            <SelectTrigger className='h-8 w-[130px] text-xs'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filters.matched.all')}</SelectItem>
              <SelectItem value='true'>{t('filters.matched.true')}</SelectItem>
              <SelectItem value='false'>
                {t('filters.matched.false')}
              </SelectItem>
            </SelectContent>
          </Select>
        )}

        {/* 5. 重置筛选按钮 */}
        {isFiltered && (
          <Button
            variant='ghost'
            size='sm'
            onClick={onResetFilters}
            className='h-8 px-2 lg:px-3 text-xs'
          >
            {t('toolbar.reset')}
            <Cross2Icon className='ms-1.5 h-3.5 w-3.5' />
          </Button>
        )}
      </div>

      {/* 右侧：视图切换与可选刷新 */}
      <div className='flex items-center gap-2'>
        <div className='flex items-center rounded-md border border-border p-0.5 bg-muted/30'>
          <Button
            variant={view === 'grid' ? 'secondary' : 'ghost'}
            size='sm'
            onClick={() => onViewChange('grid')}
            className='h-7 px-2.5 text-xs'
            title={t('views.grid')}
          >
            <LayoutGrid className='size-3.5 me-1' />
            <span>{t('views.grid')}</span>
          </Button>
          <Button
            variant={view === 'table' ? 'secondary' : 'ghost'}
            size='sm'
            onClick={() => onViewChange('table')}
            className='h-7 px-2.5 text-xs'
            title={t('views.table')}
          >
            <TableIcon className='size-3.5 me-1' />
            <span>{t('views.table')}</span>
          </Button>
        </div>

        {onRefresh && (
          <Button
            variant='outline'
            size='sm'
            onClick={onRefresh}
            disabled={isFetching}
            className='h-8 px-2.5'
            title={t('filters.refresh')}
          >
            <RotateCcw
              className={cn('size-3.5', isFetching && 'animate-spin')}
            />
          </Button>
        )}
      </div>
    </div>
  )
}
