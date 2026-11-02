import React from 'react'
import { Check, Moon, RotateCcw, SunMedium } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

export type ScheduleViewMode = 'grouped' | 'daily'

export interface SchedulePresetToolbarProps {
  disabled?: boolean
  viewMode: ScheduleViewMode
  onViewModeChange: (mode: ScheduleViewMode) => void
  onPresetAll: () => void
  onPresetWorkdays: () => void
  onPresetNights: () => void
  onPresetClear: () => void
}

export function SchedulePresetToolbar({
  disabled = false,
  viewMode,
  onViewModeChange,
  onPresetAll,
  onPresetWorkdays,
  onPresetNights,
  onPresetClear,
}: SchedulePresetToolbarProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  return (
    <div className='flex flex-wrap items-center justify-between gap-1.5 border-t border-border/50 pt-2.5'>
      <div className='flex flex-wrap items-center gap-1.5'>
        <Button
          type='button'
          size='sm'
          variant='outline'
          className='h-6 gap-1 px-2 text-[11px]'
          disabled={disabled}
          onClick={onPresetAll}
        >
          <Check className='size-3' />
          {t('schedule.presetAll', '全天')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          className='h-6 gap-1 px-2 text-[11px]'
          disabled={disabled}
          onClick={onPresetWorkdays}
        >
          <SunMedium className='size-3 text-amber-500' />
          {t('schedule.presetWorkdays', '工作日 (08:00-18:00)')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          className='h-6 gap-1 px-2 text-[11px]'
          disabled={disabled}
          onClick={onPresetNights}
        >
          <Moon className='size-3 text-indigo-400' />
          {t('schedule.presetNights', '夜间布防 (20:00-06:00)')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          className='h-6 gap-1 px-2 text-[11px] text-muted-foreground hover:text-destructive'
          disabled={disabled}
          onClick={onPresetClear}
        >
          <RotateCcw className='size-3' />
          {t('schedule.presetClear', '清空')}
        </Button>
      </div>

      {/* 模式切换 (快捷分组 / 7天明细) */}
      <div className='flex items-center rounded-md border border-border/60 bg-muted/40 p-0.5 text-[10px] font-medium'>
        <button
          type='button'
          onClick={() => onViewModeChange('grouped')}
          className={cn(
            'rounded px-2.5 py-0.5 transition-colors',
            viewMode === 'grouped'
              ? 'bg-background font-semibold text-foreground shadow-xs'
              : 'text-muted-foreground hover:text-foreground'
          )}
        >
          {t('schedule.modeGrouped', '工作日/周末')}
        </button>
        <button
          type='button'
          onClick={() => onViewModeChange('daily')}
          className={cn(
            'rounded px-2.5 py-0.5 transition-colors',
            viewMode === 'daily'
              ? 'bg-background font-semibold text-foreground shadow-xs'
              : 'text-muted-foreground hover:text-foreground'
          )}
        >
          {t('schedule.modeDaily', '按天明细')}
        </button>
      </div>
    </div>
  )
}
