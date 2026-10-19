import { useCallback, useMemo, useState } from 'react'
import { Check, Copy, Pencil, Search, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { useUpdateTask } from '../hooks/use-tasks'
import { TaskStatusBadge } from './task-status-badge'

interface TasksTableProps {
  tasks: Task[]
  cameras: Camera[]
  algorithms: Algorithm[]
  isLoading?: boolean
  search?: Record<string, unknown>
  navigate?: NavigateFn
  onEditTask: (task: Task) => void
  onDeleteTask: (task: Task) => void
}

export function TasksTable({
  tasks,
  cameras,
  algorithms,
  isLoading,
  search: searchProp,
  navigate: navigateProp,
  onEditTask,
  onDeleteTask,
}: TasksTableProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const fallbackSearch = useMemo(() => ({}), [])
  const fallbackNavigate: NavigateFn = useCallback(() => {}, [])

  const {
    globalFilter = '',
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
  } = useTableUrlState({
    search: searchProp ?? fallbackSearch,
    navigate: navigateProp ?? fallbackNavigate,
    globalFilter: { enabled: true, key: 'search' },
    columnFilters: [
      { columnId: 'camera', searchKey: 'camera', type: 'string' },
      { columnId: 'algorithm', searchKey: 'algorithm', type: 'string' },
      { columnId: 'status', searchKey: 'status', type: 'string' },
    ],
  })

  const cameraFilter =
    (columnFilters.find((f) => f.id === 'camera')?.value as string) || 'all'
  const algoFilter =
    (columnFilters.find((f) => f.id === 'algorithm')?.value as string) || 'all'
  const statusFilter =
    (columnFilters.find((f) => f.id === 'status')?.value as string) || 'all'

  const [togglingTaskId, setTogglingTaskId] = useState<string | null>(null)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const updateMutation = useUpdateTask()

  const cameraMap = useMemo(
    () => new Map(cameras.map((c) => [c.id, c])),
    [cameras]
  )
  const algoMap = useMemo(
    () => new Map(algorithms.map((a) => [a.id, a])),
    [algorithms]
  )

  const flatRows = useMemo(() => {
    const rows: {
      task: Task
      sub: Task['algorithms'][number]
      id: string
      taskId: string
      cameraId: string
      algorithmId: string
      sampleFps: number
      enabled: boolean
      isChannelEnabled: boolean
      isAlgoEnabled: boolean
      createdAt: string
      updatedAt: string
    }[] = []
    for (const task of tasks) {
      for (const sub of task.algorithms ?? []) {
        rows.push({
          task,
          sub,
          id: sub.id,
          taskId: task.id,
          cameraId: task.cameraId,
          algorithmId: sub.algorithmId,
          sampleFps: sub.sampleFps,
          enabled: task.enabled && sub.enabled,
          isChannelEnabled: task.enabled,
          isAlgoEnabled: sub.enabled,
          createdAt: sub.createdAt,
          updatedAt: sub.updatedAt,
        })
      }
    }
    return rows
  }, [tasks])

  const filteredTasks = useMemo(() => {
    return flatRows.filter((row) => {
      if (cameraFilter !== 'all' && row.cameraId !== cameraFilter) {
        return false
      }
      if (algoFilter !== 'all' && row.algorithmId !== algoFilter) {
        return false
      }
      if (statusFilter === 'running' && !row.enabled) {
        return false
      }
      if (statusFilter === 'paused' && row.enabled) {
        return false
      }

      if (globalFilter.trim()) {
        const q = globalFilter.trim().toLowerCase()
        const camName = cameraMap.get(row.cameraId)?.name.toLowerCase() || ''
        const algoName = algoMap.get(row.algorithmId)?.name.toLowerCase() || ''
        const matchesId =
          row.taskId.toLowerCase().includes(q) ||
          row.id.toLowerCase().includes(q)
        if (!matchesId && !camName.includes(q) && !algoName.includes(q)) {
          return false
        }
      }

      return true
    })
  }, [
    flatRows,
    cameraFilter,
    algoFilter,
    statusFilter,
    globalFilter,
    cameraMap,
    algoMap,
  ])

  const handleCopyId = (id: string) => {
    void navigator.clipboard.writeText(id)
    setCopiedId(id)
    toast.success(t('table.copyIdSuccess', '任务 ID 已复制到剪贴板'))
    setTimeout(() => setCopiedId(null), 2000)
  }

  const handleToggleTask = (row: (typeof flatRows)[number]) => {
    setTogglingTaskId(row.id)
    const updatedAlgos = row.task.algorithms.map((a) =>
      a.id === row.id ? { ...a, enabled: !row.isAlgoEnabled } : a
    )
    updateMutation.mutate(
      {
        id: row.taskId,
        data: {
          algorithms: updatedAlgos.map((a) => ({
            id: a.id,
            algorithmId: a.algorithmId,
            sampleFps: a.sampleFps,
            enabled: a.enabled,
            configJson: a.configJson,
            regionConfigJson: a.regionConfigJson,
          })),
        },
      },
      {
        onSuccess: () => {
          toast.success(t('table.toggleSuccess', '任务状态已更新'))
        },
        onSettled: () => {
          setTogglingTaskId(null)
        },
      }
    )
  }

  const handleCameraChange = (val: string) => {
    onColumnFiltersChange?.((prev) => [
      ...prev.filter((f) => f.id !== 'camera'),
      ...(val !== 'all' ? [{ id: 'camera', value: val }] : []),
    ])
  }

  const handleAlgoChange = (val: string) => {
    onColumnFiltersChange?.((prev) => [
      ...prev.filter((f) => f.id !== 'algorithm'),
      ...(val !== 'all' ? [{ id: 'algorithm', value: val }] : []),
    ])
  }

  const handleStatusChange = (val: string) => {
    onColumnFiltersChange?.((prev) => [
      ...prev.filter((f) => f.id !== 'status'),
      ...(val !== 'all' ? [{ id: 'status', value: val }] : []),
    ])
  }

  const handleResetFilters = () => {
    onGlobalFilterChange?.('')
    onColumnFiltersChange?.([])
  }

  return (
    <div className='space-y-4'>
      {/* 筛选过滤工具栏 */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='relative w-full max-w-sm'>
          <Search className='absolute start-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground' />
          <Input
            placeholder={t(
              'searchPlaceholder',
              '搜索任务 ID、摄像头或算法名称...'
            )}
            value={globalFilter}
            onChange={(e) => onGlobalFilterChange?.(e.target.value)}
            className='ps-9 text-xs'
          />
        </div>

        <div className='flex flex-wrap items-center gap-2'>
          {/* 相机筛选 */}
          <Select value={cameraFilter} onValueChange={handleCameraChange}>
            <SelectTrigger className='h-8 w-[140px] text-xs'>
              <SelectValue placeholder={t('filterAllCameras', '全部摄像头')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>
                {t('filterAllCameras', '全部摄像头')}
              </SelectItem>
              {cameras.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {/* 算法筛选 */}
          <Select value={algoFilter} onValueChange={handleAlgoChange}>
            <SelectTrigger className='h-8 w-[140px] text-xs'>
              <SelectValue placeholder={t('filterAllAlgorithms', '全部算法')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>
                {t('filterAllAlgorithms', '全部算法')}
              </SelectItem>
              {algorithms.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {/* 状态筛选 */}
          <Select value={statusFilter} onValueChange={handleStatusChange}>
            <SelectTrigger className='h-8 w-[110px] text-xs'>
              <SelectValue placeholder={t('filterAllStatus', '全部状态')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>
                {t('filterAllStatus', '全部状态')}
              </SelectItem>
              <SelectItem value='running'>
                {t('table.statusRunning', '运行中')}
              </SelectItem>
              <SelectItem value='paused'>
                {t('table.statusPaused', '已暂停')}
              </SelectItem>
            </SelectContent>
          </Select>

          {(globalFilter ||
            cameraFilter !== 'all' ||
            algoFilter !== 'all' ||
            statusFilter !== 'all') && (
            <Button
              variant='ghost'
              size='sm'
              className='h-8 text-xs'
              onClick={handleResetFilters}
            >
              {t('resetFilters', '重置筛选')}
            </Button>
          )}
        </div>
      </div>

      {/* 表格容器 */}
      <div className='overflow-hidden rounded-xl border border-border bg-card'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-[130px]'>
                {t('table.id', '任务 ID')}
              </TableHead>
              <TableHead>{t('table.camera', '关联摄像头')}</TableHead>
              <TableHead>{t('table.algorithm', '绑定算法包')}</TableHead>
              <TableHead className='w-[110px]'>
                {t('table.fps', '采样帧率')}
              </TableHead>
              <TableHead className='w-[100px]'>
                {t('table.computePoints', '算力开销')}
              </TableHead>
              <TableHead className='w-[140px]'>
                {t('table.status', '运行状态')}
              </TableHead>
              <TableHead className='w-[110px] text-end'>
                {t('table.actions', '操作')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: 4 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell>
                    <Skeleton className='h-4 w-20' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-32' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-28' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-12' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-12' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='h-4 w-20' />
                  </TableCell>
                  <TableCell>
                    <Skeleton className='ms-auto h-4 w-16' />
                  </TableCell>
                </TableRow>
              ))
            ) : filteredTasks.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className='h-32 text-center text-xs text-muted-foreground'
                >
                  {tasks.length === 0
                    ? t('table.empty', '暂无分析任务记录')
                    : t(
                        'table.emptyFiltered',
                        '未找到匹配的分析任务，请调整筛选条件'
                      )}
                </TableCell>
              </TableRow>
            ) : (
              filteredTasks.map((row) => {
                const camera = cameraMap.get(row.cameraId)
                const algorithm = algoMap.get(row.algorithmId)
                const cost = algorithm?.computeCost ?? 10
                const points = cost * row.sampleFps

                return (
                  <TableRow key={row.id} className='group'>
                    {/* ID 列 */}
                    <TableCell className='font-mono text-xs'>
                      <div className='flex items-center gap-1.5'>
                        <span>{row.id.slice(0, 8)}...</span>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-6 w-6 opacity-0 transition-opacity group-hover:opacity-100'
                          onClick={() => handleCopyId(row.id)}
                        >
                          {copiedId === row.id ? (
                            <Check className='h-3 w-3 text-emerald-500' />
                          ) : (
                            <Copy className='h-3 w-3 text-muted-foreground' />
                          )}
                        </Button>
                      </div>
                    </TableCell>

                    {/* 摄像头 */}
                    <TableCell className='text-xs font-medium'>
                      <div>{camera?.name || row.cameraId}</div>
                    </TableCell>

                    {/* 算法 */}
                    <TableCell className='text-xs'>
                      <div className='font-medium'>
                        {algorithm?.name || row.algorithmId}
                      </div>
                      <div className='text-[10px] text-muted-foreground'>
                        {cost} {t('dialog.costPerFrame', '点/帧')}
                      </div>
                    </TableCell>

                    {/* 采样帧率 */}
                    <TableCell className='text-xs font-medium'>
                      {row.sampleFps} FPS
                    </TableCell>

                    {/* 算力开销 */}
                    <TableCell className='font-mono text-xs font-medium'>
                      {points} {t('channelCard.pointsSuffix', '点')}
                    </TableCell>

                    {/* 状态与行内 Switch */}
                    <TableCell className='text-xs'>
                      <div className='flex items-center gap-2'>
                        <Switch
                          checked={row.enabled}
                          disabled={togglingTaskId === row.id}
                          onCheckedChange={() => handleToggleTask(row)}
                        />
                        <TaskStatusBadge enabled={row.enabled} />
                      </div>
                    </TableCell>

                    {/* 操作列 (Sticky Inset-End 防穿透) */}
                    <TableCell
                      className={cn(
                        'sticky inset-e-0 z-10 w-24 text-end',
                        'relative bg-card',
                        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
                        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
                      )}
                    >
                      <div className='flex items-center justify-end gap-1'>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-7 w-7'
                          onClick={() => onEditTask(row.task)}
                        >
                          <Pencil className='h-3.5 w-3.5' />
                        </Button>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-7 w-7 text-destructive hover:text-destructive'
                          onClick={() => onDeleteTask(row.task)}
                        >
                          <Trash2 className='h-3.5 w-3.5' />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
