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
import { Card, CardContent, CardHeader } from '@/components/ui/card'
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
    <Card className='gap-4 py-4 shadow-xs sm:py-5'>
      <CardHeader className='px-4 sm:px-5'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='flex items-center gap-2.5'>
            <div className='flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary'>
              <Cpu className='size-5' />
            </div>
            <div className='space-y-0.5'>
              <h3 className='text-sm leading-none font-semibold tracking-tight'>
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
            className={cn(
              'gap-1.5 text-xs font-normal',
              STATUS_LEVEL_CLASS[level]
            )}
          >
            <span
              className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS[level])}
            />
            {t(statusTextKey, statusTextFallback)}
          </Badge>
        </div>
      </CardHeader>

      <CardContent className='space-y-4 px-4 sm:px-5'>
        {/* 动态进度条 */}
        <div className='space-y-1.5'>
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
                STATUS_DOT_CLASS[level]
              )}
              style={{
                width: `${Math.min(100, Math.max(0, budget.percent))}%`,
              }}
            />
          </div>
        </div>

        {/* 指标统计栏目 */}
        <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          <div className='rounded-lg border border-border/60 bg-muted/30 p-2.5'>
            <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
              <Activity className='size-3.5' />
              <span>{t('budgetCard.usedPoints', '已用算力')}</span>
            </div>
            <div className='mt-1 text-base font-bold tracking-tight'>
              {budget.totalUsedPoints}
            </div>
          </div>

          <div className='rounded-lg border border-border/60 bg-muted/30 p-2.5'>
            <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
              <Cpu className='size-3.5' />
              <span>{t('budgetCard.remainingPoints', '可用余量')}</span>
            </div>
            <div
              className={cn(
                'mt-1 text-base font-bold tracking-tight',
                STATUS_TEXT_CLASS.active
              )}
            >
              {budget.remainingPoints}
            </div>
          </div>

          <div className='rounded-lg border border-border/60 bg-muted/30 p-2.5'>
            <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
              <Layers className='size-3.5' />
              <span>{t('budgetCard.activeTasks', '活跃分析')}</span>
            </div>
            <div className='mt-1 text-base font-bold tracking-tight'>
              {budget.enabledCount}
            </div>
          </div>

          <div className='rounded-lg border border-border/60 bg-muted/30 p-2.5'>
            <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
              <Layers className='size-3.5' />
              <span>{t('budgetCard.totalTasks', '总配置数')}</span>
            </div>
            <div className='mt-1 text-base font-bold tracking-tight'>
              {budget.totalCount}
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
