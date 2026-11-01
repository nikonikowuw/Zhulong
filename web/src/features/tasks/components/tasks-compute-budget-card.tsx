import { Activity, Cpu, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  type StatusLevel,
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  STATUS_TEXT_CLASS,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type TaskComputeBudget } from '../hooks/use-task-compute-budget'

interface TasksComputeBudgetCardProps {
  budget: TaskComputeBudget
}

/**
 * 显式映射利用率区间 → 四级状态语义（§1.3.3），未声明的区间统一回落 `active`。
 */
function budgetStatusLevel(budget: TaskComputeBudget): StatusLevel {
  if (budget.isOverBudget) return 'destructive'
  if (budget.isWarning) return 'warning'
  return 'active'
}

const BUDGET_STATUS_TEXT_KEY = {
  active: ['budgetCard.statusHealthy', '算力充裕'],
  warning: ['budgetCard.statusWarning', '高负荷警戒'],
  destructive: ['budgetCard.statusOverload', '算力超载'],
  inactive: ['budgetCard.statusUnknown', '算力未知'],
} as const

export function TasksComputeBudgetCard({
  budget,
}: TasksComputeBudgetCardProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const level = budgetStatusLevel(budget)
  const [statusTextKey, statusTextFallback] = BUDGET_STATUS_TEXT_KEY[level]

  return (
    <div className='flex flex-col gap-3 rounded-xl border border-border/80 bg-card/60 p-3.5 shadow-xs backdrop-blur-xs transition-all sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-4 sm:py-2.5'>
      {/* 左侧：精简状态标识与标题 */}
      <div className='flex shrink-0 items-center gap-3'>
        <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/60 text-muted-foreground'>
          <Cpu className='size-4' />
        </div>
        <div className='flex items-center gap-2'>
          <span className='text-sm font-semibold tracking-tight text-foreground'>
            {t('budgetCard.title', 'NPU 算力调度')}
          </span>
          <Badge
            variant='outline'
            className={cn(
              'h-5 gap-1 px-1.5 text-[11px] font-normal',
              STATUS_LEVEL_CLASS[level]
            )}
          >
            <span
              className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS[level])}
            />
            {t(statusTextKey, statusTextFallback)}
          </Badge>
        </div>
      </div>

      {/* 中部：微型利用率进度条与点数 */}
      <div className='flex min-w-[200px] flex-1 flex-col gap-1 sm:max-w-md'>
        <div className='flex items-center justify-between text-xs'>
          <div className='flex items-center gap-1.5'>
            <span className='font-medium text-foreground'>
              {budget.peakUsedPoints ?? budget.totalUsedPoints} /{' '}
              {budget.maxPoints} {t('channelCard.pointsSuffix', '点')}
            </span>
            <span className='text-[11px] text-muted-foreground'>
              ({t('budgetCard.currentPoints', '瞬时')}:{' '}
              <strong className='font-normal text-foreground'>
                {budget.currentUsedPoints ?? budget.totalUsedPoints}
              </strong>
              )
            </span>
          </div>
          <span className='font-mono text-muted-foreground'>
            {budget.percent}%
          </span>
        </div>
        <div className='h-1.5 w-full overflow-hidden rounded-full bg-muted'>
          <div
            className={cn(
              'h-full transition-all duration-300',
              STATUS_DOT_CLASS[level]
            )}
            style={{
              width: `${Math.min(100, Math.max(0, budget.percent))}%`,
            }}
          />
        </div>
      </div>

      {/* 右侧：紧凑指标胶囊 */}
      <div className='flex shrink-0 flex-wrap items-center gap-2 text-xs sm:gap-3'>
        <div className='flex items-center gap-1.5 rounded-md bg-muted/50 px-2 py-1 text-muted-foreground'>
          <Activity className='size-3 text-muted-foreground/80' />
          <span>{t('budgetCard.remainingPoints', '可用余量')}:</span>
          <span
            className={cn(
              'font-semibold tracking-tight',
              STATUS_TEXT_CLASS.active
            )}
          >
            {budget.remainingPoints}
          </span>
        </div>

        <div className='flex items-center gap-1.5 rounded-md bg-muted/50 px-2 py-1 text-muted-foreground'>
          <Layers className='size-3 text-muted-foreground/80' />
          <span>{t('budgetCard.activeTasks', '活跃')}:</span>
          <span className='font-semibold tracking-tight text-foreground'>
            {budget.enabledCount}
          </span>
          <span className='text-muted-foreground/60'>/</span>
          <span className='text-muted-foreground'>{budget.totalCount}</span>
        </div>
      </div>
    </div>
  )
}
