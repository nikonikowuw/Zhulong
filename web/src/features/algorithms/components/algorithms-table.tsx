import { useState, useMemo } from 'react'
import {
  Cpu,
  Layers,
  Search,
  Trash2,
  UploadCloud,
  HardDrive,
  Activity,
  RotateCcw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { categoryBadgeClass, type CategoryHue } from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type Algorithm, type AlgorithmTaskType } from '../data/schema'
import {
  useAlgorithms,
  useEnableAlgorithm,
  useDisableAlgorithm,
} from '../hooks/use-algorithms'
import { AlgorithmDeleteDialog } from './algorithm-delete-dialog'
import { AlgorithmResetDialog } from './algorithm-reset-dialog'
import { AlgorithmStatusBadge } from './algorithm-status-badge'

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

interface AlgorithmsTableProps {
  onOpenUpload: () => void
}

export function AlgorithmsTable({
  onOpenUpload,
}: AlgorithmsTableProps): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const { data: algorithms = [], isLoading, error, refetch } = useAlgorithms()

  const [searchQuery, setSearchQuery] = useState('')
  const [taskTypeFilter, setTaskTypeFilter] = useState<string>('all')
  const [deletingAlgorithm, setDeletingAlgorithm] = useState<Algorithm | null>(
    null
  )
  const [resettingAlgorithm, setResettingAlgorithm] =
    useState<Algorithm | null>(null)

  const enableMutation = useEnableAlgorithm()
  const disableMutation = useDisableAlgorithm()

  const filteredAlgorithms = useMemo(() => {
    let result = algorithms

    if (taskTypeFilter !== 'all') {
      result = result.filter((a) => a.taskType === taskTypeFilter)
    }

    if (searchQuery.trim()) {
      const q = searchQuery.trim().toLowerCase()
      result = result.filter(
        (a) =>
          a.name.toLowerCase().includes(q) ||
          a.id.toLowerCase().includes(q) ||
          a.description.toLowerCase().includes(q)
      )
    }

    return result
  }, [algorithms, taskTypeFilter, searchQuery])

  const handleToggleStatus = (algo: Algorithm) => {
    // 熔断态由熔断器封禁，启用必然 409 ALGORITHM_IN_CRASH_LOOP；
    // 开关已 disabled，这里再兜一层防御，避免后续改动绕过 UI 约束。
    if (algo.status === 'CRASH_LOOP') return

    if (algo.status === 'ACTIVE' || algo.status === 'RUNNING') {
      disableMutation.mutate(algo.id)
    } else {
      enableMutation.mutate(algo.id)
    }
  }

  const getTaskTypeBadge = (taskType: AlgorithmTaskType) => {
    /** 任务类型是「分类标签」，走 §1.3.6 分类色板；未声明的类型串回落中性兜底。 */
    const item = TASK_TYPE_BADGE[taskType]
    const labelKey = item?.labelKey ?? taskType
    const className = categoryBadgeClass(item?.hue)
    return (
      <Badge variant='outline' className={cn('px-2 py-0.5 text-xs', className)}>
        {t(labelKey, taskType)}
      </Badge>
    )
  }

  // 1. 错误态
  if (error) {
    return (
      <div className='flex flex-1 flex-col items-center justify-center p-8 text-center'>
        <p className='text-sm text-destructive'>
          {t('fetchFailed', '获取算法包列表失败')}
        </p>
        <Button
          variant='outline'
          size='sm'
          className='mt-3'
          onClick={() => void refetch()}
        >
          {t('retry', '重试')}
        </Button>
      </div>
    )
  }

  // 2. 加载骨架屏
  if (isLoading) {
    return (
      <div className='flex flex-1 flex-col gap-3 p-4'>
        <div className='flex gap-2'>
          <Skeleton className='h-9 w-64' />
          <Skeleton className='h-9 w-32' />
        </div>
        <div className='flex flex-col gap-2'>
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} className='h-16 w-full rounded-xl' />
          ))}
        </div>
      </div>
    )
  }

  // 3. 空资产态（没有任何算法）
  if (algorithms.length === 0) {
    return (
      <div className='flex flex-1 flex-col items-center justify-center rounded-xl border border-dashed border-border/80 p-12 text-center'>
        <div className='mb-3 rounded-full bg-primary/10 p-4 text-primary'>
          <Layers className='h-8 w-8' />
        </div>
        <h3 className='text-base font-semibold text-foreground'>
          {t('emptyAlgorithmsTitle', '尚未安装任何算法包')}
        </h3>
        <p className='mt-1 max-w-sm text-xs text-muted-foreground'>
          {t(
            'emptyAlgorithmsDesc',
            '支持目标检测、图像分类、人脸与结构化算法包。上传符合规范的 .tar.gz 或 .zip 归档即可快速启用。'
          )}
        </p>
        <Button className='mt-4 gap-2' onClick={onOpenUpload}>
          <UploadCloud className='h-4 w-4' />
          {t('uploadFirstAlgorithm', '安装第一个算法包')}
        </Button>
      </div>
    )
  }

  return (
    <div className='flex flex-1 flex-col gap-4'>
      {/* 搜索与类型过滤工具栏 */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <div className='relative w-64'>
            <Search className='absolute start-2.5 top-2.5 h-4 w-4 text-muted-foreground' />
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={t('searchPlaceholder', '搜索算法名称或标识...')}
              className='h-9 ps-8 text-xs'
            />
          </div>

          <div className='flex items-center rounded-lg border border-border bg-muted/40 p-0.5 text-xs font-medium'>
            {[
              { key: 'all', labelKey: 'filterAll' },
              { key: 'detection', labelKey: 'taskDetection' },
              { key: 'person_structure', labelKey: 'taskPersonStructure' },
              { key: 'vehicle_structure', labelKey: 'taskVehicleStructure' },
              { key: 'classification', labelKey: 'taskClassification' },
            ].map((tab) => {
              const isActive = taskTypeFilter === tab.key
              return (
                <button
                  key={tab.key}
                  type='button'
                  onClick={() => setTaskTypeFilter(tab.key)}
                  className={`rounded-md px-2.5 py-1 transition-all ${
                    isActive
                      ? 'bg-background font-semibold text-foreground shadow-xs'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {t(tab.labelKey, tab.key)}
                </button>
              )
            })}
          </div>
        </div>

        <div className='text-xs text-muted-foreground'>
          {t('totalCount', '共 {{count}} 个算法包', {
            count: filteredAlgorithms.length,
          })}
        </div>
      </div>

      {/* 过滤无结果态 */}
      {filteredAlgorithms.length === 0 ? (
        <div className='flex flex-1 flex-col items-center justify-center rounded-xl border border-border/60 p-8 text-center'>
          <p className='text-xs text-muted-foreground'>
            {t('noMatchingAlgorithms', '没有找到符合条件的算法包')}
          </p>
          <Button
            variant='ghost'
            size='sm'
            className='mt-2 text-xs'
            onClick={() => {
              setSearchQuery('')
              setTaskTypeFilter('all')
            }}
          >
            {t('clearFilter', '清空筛选条件')}
          </Button>
        </div>
      ) : (
        /* 算法包列表表格 */
        <div className='overflow-hidden rounded-xl border border-border bg-card shadow-xs'>
          <Table>
            <TableHeader>
              <TableRow className='bg-muted/40 hover:bg-muted/40'>
                <TableHead>{t('thAlgorithm', '算法名称与标识')}</TableHead>
                <TableHead>{t('thType', '任务形态')}</TableHead>
                <TableHead>{t('thPlatform', '目标平台')}</TableHead>
                <TableHead>{t('thResources', '资源消耗')}</TableHead>
                <TableHead>{t('thStatus', '运行状态')}</TableHead>
                <TableHead>{t('thEnable', '调度开关')}</TableHead>
                <TableHead className='text-end'>
                  {t('thActions', '操作')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filteredAlgorithms.map((algo) => {
                const isOperating =
                  enableMutation.isPending || disableMutation.isPending
                const isEnabled =
                  algo.status === 'ACTIVE' || algo.status === 'RUNNING'
                const isCrashLoop = algo.status === 'CRASH_LOOP'

                return (
                  <TableRow key={algo.id} className='transition-colors'>
                    {/* 名称与版本 */}
                    <TableCell>
                      <div className='flex flex-col'>
                        <div className='flex items-center gap-1.5'>
                          <span className='text-sm font-semibold text-foreground'>
                            {algo.name}
                          </span>
                          <span className='font-mono text-xs text-muted-foreground'>
                            v{algo.version}
                          </span>
                        </div>
                        <span className='font-mono text-[11px] text-muted-foreground'>
                          {algo.id}
                        </span>
                        {algo.description && (
                          <span className='mt-0.5 line-clamp-1 text-xs text-muted-foreground/80'>
                            {algo.description}
                          </span>
                        )}
                      </div>
                    </TableCell>

                    {/* 任务形态 */}
                    <TableCell>{getTaskTypeBadge(algo.taskType)}</TableCell>

                    {/* 目标平台 */}
                    <TableCell>
                      <div className='flex items-center gap-1.5 font-mono text-xs text-foreground'>
                        <Cpu className='h-3.5 w-3.5 text-muted-foreground' />
                        <span>{algo.targetArch}</span>
                        <span className='text-muted-foreground'>/</span>
                        <span className='font-semibold text-primary uppercase'>
                          {algo.targetDevice}
                        </span>
                      </div>
                    </TableCell>

                    {/* 资源消耗 */}
                    <TableCell>
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
                    </TableCell>

                    {/* 运行状态 */}
                    <TableCell>
                      <AlgorithmStatusBadge status={algo.status} />
                    </TableCell>

                    {/* 启停开关 */}
                    <TableCell>
                      <Switch
                        checked={isEnabled}
                        disabled={isOperating || isCrashLoop}
                        onCheckedChange={() => handleToggleStatus(algo)}
                        title={
                          isCrashLoop
                            ? t(
                                'crashLoopSwitchHint',
                                '算法处于熔断状态，请先重置熔断'
                              )
                            : isEnabled
                              ? t('clickToDisable', '点击停用')
                              : t('clickToEnable', '点击启用')
                        }
                      />
                    </TableCell>

                    {/* 操作 (熔断复位 / 卸载删除) */}
                    <TableCell className='text-end'>
                      <div className='flex items-center justify-end gap-1'>
                        {isCrashLoop && (
                          <Button
                            variant='outline'
                            size='sm'
                            className='h-8 gap-1.5 text-xs'
                            onClick={() => setResettingAlgorithm(algo)}
                          >
                            <RotateCcw className='h-3.5 w-3.5' />
                            {t('resetCrashLoop', '重置熔断')}
                          </Button>
                        )}
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-8 w-8 text-muted-foreground hover:bg-destructive/10 hover:text-destructive'
                          disabled={isEnabled || isOperating}
                          title={
                            isEnabled
                              ? t(
                                  'disableBeforeDelete',
                                  '请先停用算法包后再执行卸载'
                                )
                              : t('deleteAlgorithm', '卸载算法包')
                          }
                          onClick={() => setDeletingAlgorithm(algo)}
                        >
                          <Trash2 className='h-4 w-4' />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}

      {/* 删除确认弹窗 */}
      <AlgorithmDeleteDialog
        algorithm={deletingAlgorithm}
        open={Boolean(deletingAlgorithm)}
        onOpenChange={(open) => {
          if (!open) setDeletingAlgorithm(null)
        }}
      />

      {/* 熔断复位弹窗 */}
      <AlgorithmResetDialog
        algorithm={resettingAlgorithm}
        open={Boolean(resettingAlgorithm)}
        onOpenChange={(open) => {
          if (!open) setResettingAlgorithm(null)
        }}
      />
    </div>
  )
}
