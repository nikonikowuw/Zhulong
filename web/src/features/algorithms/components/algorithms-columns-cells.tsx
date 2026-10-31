import { type Column } from '@tanstack/react-table'
import { Activity, Cpu, HardDrive } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { categoryBadgeClass, type CategoryHue } from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { DataTableColumnHeader } from '@/components/data-table'
import {
  type Algorithm,
  type AlgorithmStatus,
  type AlgorithmTaskType,
} from '../data/schema'
import {
  useEnableAlgorithm,
  useDisableAlgorithm,
} from '../hooks/use-algorithms'
import { AlgorithmStatusBadge } from './algorithm-status-badge'

export function AlgorithmColumnHeader({
  column,
  titleKey,
  defaultTitle,
}: {
  column: Column<Algorithm, unknown>
  titleKey: string
  defaultTitle: string
}) {
  const { t } = useTranslation('algorithms')
  return (
    <DataTableColumnHeader column={column} title={t(titleKey, defaultTitle)} />
  )
}

/** 任务类型 → §1.3.6 分类色板槽位（显式映射，避免新增分类时重排既有颜色）。 */
const TASK_TYPE_BADGE: Record<
  AlgorithmTaskType,
  { labelKey: string; hue: CategoryHue }
> = {
  detection: { labelKey: 'taskDetection', hue: 'blue' },
  classification: { labelKey: 'taskClassification', hue: 'violet' },
  person_structure: { labelKey: 'taskPersonStructure', hue: 'emerald' },
  vehicle_structure: { labelKey: 'taskVehicleStructure', hue: 'fuchsia' },
}

export function AlgorithmTaskTypeCell({
  taskType,
}: {
  taskType: AlgorithmTaskType
}) {
  const { t } = useTranslation('algorithms')
  const item = TASK_TYPE_BADGE[taskType]
  const labelKey = item?.labelKey ?? taskType
  const className = categoryBadgeClass(item?.hue)
  return (
    <Badge variant='outline' className={cn('px-2 py-0.5 text-xs', className)}>
      {t(labelKey, taskType)}
    </Badge>
  )
}

export function AlgorithmPlatformCell({ algo }: { algo: Algorithm }) {
  return (
    <div className='flex items-center gap-1.5 font-mono text-xs text-foreground'>
      <Cpu className='h-3.5 w-3.5 text-muted-foreground' />
      <span>{algo.targetArch}</span>
      <span className='text-muted-foreground'>/</span>
      <span className='font-semibold text-primary uppercase'>
        {algo.targetDevice}
      </span>
    </div>
  )
}

export function AlgorithmResourcesCell({ algo }: { algo: Algorithm }) {
  const { t } = useTranslation('algorithms')
  return (
    <div className='flex flex-col gap-0.5 text-xs text-muted-foreground'>
      <div className='flex items-center gap-1'>
        <Activity className='h-3 w-3' />
        <span>
          {t('computeCostLabel', '算力:')}{' '}
          <strong className='font-mono font-semibold text-foreground'>
            {algo.computeCost}
          </strong>{' '}
          {t('points', '点')}
        </span>
      </div>
      <div className='flex items-center gap-1'>
        <HardDrive className='h-3 w-3' />
        <span>
          {t('memoryLabel', '内存:')}{' '}
          <strong className='font-mono font-semibold text-foreground'>
            {algo.memoryMb}
          </strong>{' '}
          MB
        </span>
      </div>
    </div>
  )
}

export function AlgorithmStatusCell({ status }: { status: AlgorithmStatus }) {
  return <AlgorithmStatusBadge status={status} />
}

export function AlgorithmSwitchCell({ algo }: { algo: Algorithm }) {
  const { t } = useTranslation('algorithms')
  const enableMutation = useEnableAlgorithm()
  const disableMutation = useDisableAlgorithm()

  const isOperating = enableMutation.isPending || disableMutation.isPending
  const isEnabled = algo.status === 'ACTIVE' || algo.status === 'RUNNING'
  const isCrashLoop = algo.status === 'CRASH_LOOP'

  const handleToggle = () => {
    if (isCrashLoop || isOperating) return
    if (isEnabled) {
      disableMutation.mutate(algo.id)
    } else {
      enableMutation.mutate(algo.id)
    }
  }

  return (
    <Switch
      checked={isEnabled}
      disabled={isOperating || isCrashLoop}
      onCheckedChange={handleToggle}
      title={
        isCrashLoop
          ? t('crashLoopSwitchHint', '算法处于熔断状态，请先重置熔断')
          : isEnabled
            ? t('clickToDisable', '点击停用')
            : t('clickToEnable', '点击启用')
      }
    />
  )
}
