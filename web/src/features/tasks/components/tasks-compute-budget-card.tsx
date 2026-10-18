import { Activity, Cpu, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { type TaskComputeBudget } from '../hooks/use-task-compute-budget'

interface TasksComputeBudgetCardProps {
  budget: TaskComputeBudget
}

export function TasksComputeBudgetCard({
  budget,
}: TasksComputeBudgetCardProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  // 根据利用率判断状态徽标样式
  let statusBadgeStyle = STATUS_LEVEL_CLASS.active
  let dotColor = STATUS_DOT_CLASS.active
  let statusText = t('budgetCard.statusHealthy', '算力充裕')
  let progressBarColor = 'bg-emerald-500'

  if (budget.isOverBudget) {
    statusBadgeStyle = STATUS_LEVEL_CLASS.destructive
    dotColor = STATUS_DOT_CLASS.destructive
    statusText = t('budgetCard.statusOverload', '算力超载')
    progressBarColor = 'bg-destructive'
  } else if (budget.isWarning) {
    statusBadgeStyle = STATUS_LEVEL_CLASS.warning
    dotColor = STATUS_DOT_CLASS.warning
    statusText = t('budgetCard.statusWarning', '高负荷警戒')
    progressBarColor = 'bg-amber-500'
  }

  return (
    <div className='relative overflow-hidden rounded-xl border border-border bg-card p-4 shadow-xs sm:p-5'>
      {/* 顶部标题与状态标签 */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex items-center gap-2.5'>
          <div className='flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-muted/40'>
            <Cpu className='h-5 w-5 text-primary' />
          </div>
          <div>
            <h3 className='text-sm font-semibold tracking-tight'>
              {t('budgetCard.title', '算力配额与调度看板')}
            </h3>
            <p className='text-xs text-muted-foreground'>
              {t(
                'budgetCard.desc',
                '全平台共享 1,000 点 NPU 异构加速算力池，基于已启用任务实时核算。'
              )}
            </p>
          </div>
        </div>

        <Badge
          variant='outline'
          className={cn('gap-1.5 text-xs font-normal', statusBadgeStyle)}
        >
          <span className={cn('h-1.5 w-1.5 rounded-full', dotColor)} />
          {statusText}
        </Badge>
      </div>

      {/* 动态进度条 */}
      <div className='mt-4 space-y-1.5'>
        <div className='flex items-center justify-between text-xs'>
          <span className='font-medium text-foreground'>
            {budget.totalUsedPoints} / {budget.maxPoints}{' '}
            {t('channelCard.pointsSuffix', '点')}
          </span>
          <span className='text-muted-foreground'>{budget.percent}%</span>
        </div>
        <div className='h-2 w-full overflow-hidden rounded-full bg-muted/50'>
          <div
            className={cn(
              'h-full transition-all duration-300',
              progressBarColor
            )}
            style={{ width: `${Math.min(100, Math.max(0, budget.percent))}%` }}
          />
        </div>
      </div>

      {/* 指标统计栏目 */}
      <div className='mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4'>
        <div className='rounded-lg border border-border/60 bg-muted/20 p-2.5'>
          <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            <Activity className='h-3.5 w-3.5' />
            <span>{t('budgetCard.usedPoints', '已用算力')}</span>
          </div>
          <div className='mt-1 text-base font-bold tracking-tight'>
            {budget.totalUsedPoints}
          </div>
        </div>

        <div className='rounded-lg border border-border/60 bg-muted/20 p-2.5'>
          <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            <Cpu className='h-3.5 w-3.5' />
            <span>{t('budgetCard.remainingPoints', '可用余量')}</span>
          </div>
          <div className='mt-1 text-base font-bold tracking-tight text-emerald-600 dark:text-emerald-400'>
            {budget.remainingPoints}
          </div>
        </div>

        <div className='rounded-lg border border-border/60 bg-muted/20 p-2.5'>
          <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            <Layers className='h-3.5 w-3.5' />
            <span>{t('budgetCard.activeTasks', '活跃分析')}</span>
          </div>
          <div className='mt-1 text-base font-bold tracking-tight'>
            {budget.enabledCount}
          </div>
        </div>

        <div className='rounded-lg border border-border/60 bg-muted/20 p-2.5'>
          <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            <Layers className='h-3.5 w-3.5' />
            <span>{t('budgetCard.totalTasks', '总配置数')}</span>
          </div>
          <div className='mt-1 text-base font-bold tracking-tight'>
            {budget.totalCount}
          </div>
        </div>
      </div>
    </div>
  )
}
