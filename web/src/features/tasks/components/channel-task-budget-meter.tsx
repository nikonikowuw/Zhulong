import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_TEXT_CLASS,
  type StatusLevel,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'

export interface ChannelTaskBudgetMeterProps {
  /** 本通道当前占用点数。 */
  channelPoints: number
  /** 全平台预计占用（本通道 + 其它通道）。 */
  projectedPoints: number
  maxPoints: number
  isOverBudget: boolean
}

/** 顶部控制台的算力占用条：左侧本通道，右侧全平台预算。 */
export function ChannelTaskBudgetMeter({
  channelPoints,
  projectedPoints,
  maxPoints,
  isOverBudget,
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
    <div className='hidden flex-col gap-1 lg:flex'>
      <div className='flex items-center justify-between font-mono text-xs'>
        <span className='text-muted-foreground'>
          {t('dialog.channelPointsPreview', '本通道算力')}:{' '}
          <strong className='text-foreground'>{channelPoints}</strong>{' '}
          {t('channelCard.pointsSuffix', '点')}
        </span>
        <span
          className={cn(
            'ms-4 font-semibold',
            isOverBudget
              ? 'font-bold text-destructive'
              : isWarning
                ? STATUS_TEXT_CLASS.warning
                : 'text-muted-foreground'
          )}
        >
          {projectedPoints} / {maxPoints} {t('channelCard.pointsSuffix', '点')}{' '}
          <span className='text-[11px] font-normal'>({percent}%)</span>
        </span>
      </div>
      <div className='h-1.5 w-52 overflow-hidden rounded-full bg-muted'>
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
