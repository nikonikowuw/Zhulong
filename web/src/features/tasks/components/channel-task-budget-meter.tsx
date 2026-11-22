import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_TEXT_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'

export interface ChannelTaskBudgetMeterProps {
  /** 本通道峰值占用点数。 */
  channelPoints: number
  /** 全平台预计峰值占用（本通道 + 其它通道）。 */
  projectedPoints: number
  maxPoints: number
  isOverBudget: boolean
  /** 本通道当前瞬时占用点数。 */
  channelCurrentPoints?: number
  /** 全平台当前瞬时占用点数。 */
  projectedCurrentPoints?: number
  className?: string
}

/** 顶部控制台的算力占用条：左侧本通道，右侧全平台预算。 */
export function ChannelTaskBudgetMeter({
  channelPoints,
  projectedPoints,
  maxPoints,
  isOverBudget,
  channelCurrentPoints,
  projectedCurrentPoints,
  className,
}: ChannelTaskBudgetMeterProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const percent = Math.min(100, Math.round((projectedPoints / maxPoints) * 100))
  const isWarning = !isOverBudget && percent >= 85
  const level: StatusLevel = isOverBudget
    ? 'destructive'
    : isWarning
      ? 'warning'
      : 'active'

  return (
    <div className={cn('flex w-full flex-col gap-1 lg:w-auto', className)}>
      <div className='flex flex-wrap items-center justify-between gap-x-4 gap-y-0.5 font-mono text-xs'>
        <span className='text-muted-foreground'>
          {t('dialog.channelPointsPreview', '本通道算力')}:{' '}
          <strong className='text-foreground'>{channelPoints}</strong>
          {channelCurrentPoints !== undefined && (
            <span className='font-normal text-muted-foreground'>
              {' '}
              ({t('dialog.channelCurrentPoints', '瞬时')}:{channelCurrentPoints}
              )
            </span>
          )}{' '}
          {t('channelCard.pointsSuffix', '点')}
        </span>
        <span
          className={cn(
            'font-semibold lg:ms-4',
            isOverBudget
              ? 'font-bold text-destructive'
              : isWarning
                ? STATUS_TEXT_CLASS.warning
                : 'text-muted-foreground'
          )}
        >
          {projectedPoints} / {maxPoints} {t('channelCard.pointsSuffix', '点')}
          {projectedCurrentPoints !== undefined && (
            <span className='font-normal text-muted-foreground'>
              {' '}
              ({t('dialog.projectedCurrentPoints', '瞬时')}:
              {projectedCurrentPoints})
            </span>
          )}{' '}
          <span className='text-[11px] font-normal'>({percent}%)</span>
        </span>
      </div>
      <div className='h-1.5 w-full overflow-hidden rounded-full bg-muted lg:w-52'>
        <div
          style={{ width: `${percent}%` }}
          className={cn(
            'h-full transition-all duration-300',
            STATUS_DOT_CLASS[level]
          )}
        />
      </div>
    </div>
  )
}
