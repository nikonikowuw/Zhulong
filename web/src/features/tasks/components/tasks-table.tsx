import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Copy, Pencil, Search, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
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
import { cn } from '@/lib/utils'
import { type Task } from '../data/schema'
import { useUpdateTask } from '../hooks/use-tasks'
import { TaskStatusBadge } from './task-status-badge'

interface TasksTableProps {
  tasks: Task[]
  cameras: Camera[]
  algorithms: Algorithm[]
  isLoading?: boolean
  onEditTask: (task: Task) => void
  onDeleteTask: (task: Task) => void
}

export function TasksTable({
  tasks,
  cameras,
  algorithms,
  isLoading,
  onEditTask,
  onDeleteTask,
}: TasksTableProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const [search, setSearch] = useState('')
  const [cameraFilter, setCameraFilter] = useState('all')
  const [algoFilter, setAlgoFilter] = useState('all')
  const [statusFilter, setStatusFilter] = useState('all')
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const updateMutation = useUpdateTask()

  const cameraMap = useMemo(() => new Map(cameras.map((c) => [c.id, c])), [cameras])
  const algoMap = useMemo(() => new Map(algorithms.map((a) => [a.id, a])), [algorithms])

  const filteredTasks = useMemo(() => {
    return tasks.filter((task) => {
      if (cameraFilter !== 'all' && task.cameraId !== cameraFilter) {
        return false
      }
      if (algoFilter !== 'all' && task.algorithmId !== algoFilter) {
        return false
      }
      if (statusFilter === 'running' && !task.enabled) {
        return false
      }
      if (statusFilter === 'paused' && task.enabled) {
        return false
      }

      if (search.trim()) {
        const q = search.trim().toLowerCase()
        const camName = cameraMap.get(task.cameraId)?.name.toLowerCase() || ''
        const algoName = algoMap.get(task.algorithmId)?.name.toLowerCase() || ''
        const matchesId = task.id.toLowerCase().includes(q)
        if (!matchesId && !camName.includes(q) && !algoName.includes(q)) {
          return false
        }
      }

      return true
    })
  }, [tasks, cameraFilter, algoFilter, statusFilter, search, cameraMap, algoMap])

  const handleCopyId = (id: string) => {
    void navigator.clipboard.writeText(id)
    setCopiedId(id)
    toast.success(t('table.copyIdSuccess', '任务 ID 已复制到剪贴板'))
    setTimeout(() => setCopiedId(null), 2000)
  }

  const handleToggleTask = (task: Task) => {
    updateMutation.mutate(
      {
        id: task.id,
        data: { enabled: !task.enabled },
      },
      {
        onSuccess: () => {
          toast.success(t('table.toggleSuccess', '任务状态已更新'))
        },
        onError: (err) => {
          toast.error(err.message || t('table.toggleError', '更新任务状态失败'))
        },
      }
    )
  }

  return (
    <div className='space-y-4'>
      {/* 筛选过滤工具栏 */}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='relative w-full max-w-sm'>
          <Search className='absolute start-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground' />
          <Input
            placeholder={t('searchPlaceholder', '搜索任务 ID、摄像头或算法名称...')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className='ps-9 text-xs'
          />
        </div>

        <div className='flex flex-wrap items-center gap-2'>
          {/* 相机筛选 */}
          <Select value={cameraFilter} onValueChange={setCameraFilter}>
            <SelectTrigger className='h-8 w-[140px] text-xs'>
              <SelectValue placeholder={t('filterAllCameras', '全部摄像头')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filterAllCameras', '全部摄像头')}</SelectItem>
              {cameras.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {/* 算法筛选 */}
          <Select value={algoFilter} onValueChange={setAlgoFilter}>
            <SelectTrigger className='h-8 w-[140px] text-xs'>
              <SelectValue placeholder={t('filterAllAlgorithms', '全部算法')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filterAllAlgorithms', '全部算法')}</SelectItem>
              {algorithms.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {/* 状态筛选 */}
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className='h-8 w-[110px] text-xs'>
              <SelectValue placeholder={t('filterAllStatus', '全部状态')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('filterAllStatus', '全部状态')}</SelectItem>
              <SelectItem value='running'>{t('table.statusRunning', '运行中')}</SelectItem>
              <SelectItem value='paused'>{t('table.statusPaused', '已暂停')}</SelectItem>
            </SelectContent>
          </Select>

          {(search || cameraFilter !== 'all' || algoFilter !== 'all' || statusFilter !== 'all') && (
            <Button
              variant='ghost'
              size='sm'
              className='h-8 text-xs'
              onClick={() => {
                setSearch('')
                setCameraFilter('all')
                setAlgoFilter('all')
                setStatusFilter('all')
              }}
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
              <TableHead className='w-[130px]'>{t('table.id', '任务 ID')}</TableHead>
              <TableHead>{t('table.camera', '关联摄像头')}</TableHead>
              <TableHead>{t('table.algorithm', '绑定算法包')}</TableHead>
              <TableHead className='w-[110px]'>{t('table.fps', '采样帧率')}</TableHead>
              <TableHead className='w-[100px]'>{t('table.computePoints', '算力开销')}</TableHead>
              <TableHead className='w-[140px]'>{t('table.status', '运行状态')}</TableHead>
              <TableHead className='w-[110px] text-end'>{t('table.actions', '操作')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: 4 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell><Skeleton className='h-4 w-20' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-32' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-28' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-12' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-12' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-20' /></TableCell>
                  <TableCell><Skeleton className='h-4 w-16 ms-auto' /></TableCell>
                </TableRow>
              ))
            ) : filteredTasks.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className='h-32 text-center text-xs text-muted-foreground'>
                  {tasks.length === 0
                    ? t('table.empty', '暂无分析任务记录')
                    : t('table.emptyFiltered', '未找到匹配的分析任务，请调整筛选条件')}
                </TableCell>
              </TableRow>
            ) : (
              filteredTasks.map((task) => {
                const camera = cameraMap.get(task.cameraId)
                const algorithm = algoMap.get(task.algorithmId)
                const cost = algorithm?.computeCost ?? 10
                const points = cost * task.sampleFps

                return (
                  <TableRow key={task.id} className='group'>
                    {/* ID 列 */}
                    <TableCell className='font-mono text-xs'>
                      <div className='flex items-center gap-1.5'>
                        <span>{task.id.slice(0, 8)}...</span>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-6 w-6 opacity-0 group-hover:opacity-100 transition-opacity'
                          onClick={() => handleCopyId(task.id)}
                        >
                          {copiedId === task.id ? (
                            <Check className='h-3 w-3 text-emerald-500' />
                          ) : (
                            <Copy className='h-3 w-3 text-muted-foreground' />
                          )}
                        </Button>
                      </div>
                    </TableCell>

                    {/* 摄像头 */}
                    <TableCell className='text-xs font-medium'>
                      <div>{camera?.name || task.cameraId}</div>
                    </TableCell>

                    {/* 算法 */}
                    <TableCell className='text-xs'>
                      <div className='font-medium'>{algorithm?.name || task.algorithmId}</div>
                      <div className='text-[10px] text-muted-foreground'>
                        {cost} {t('dialog.costPerFrame', '点/帧')}
                      </div>
                    </TableCell>

                    {/* 采样帧率 */}
                    <TableCell className='text-xs font-medium'>
                      {task.sampleFps} FPS
                    </TableCell>

                    {/* 算力开销 */}
                    <TableCell className='text-xs font-mono font-medium'>
                      {points} {t('channelCard.pointsSuffix', '点')}
                    </TableCell>

                    {/* 状态与行内 Switch */}
                    <TableCell className='text-xs'>
                      <div className='flex items-center gap-2'>
                        <Switch
                          checked={task.enabled}
                          disabled={updateMutation.isPending}
                          onCheckedChange={() => handleToggleTask(task)}
                        />
                        <TaskStatusBadge enabled={task.enabled} />
                      </div>
                    </TableCell>

                    {/* 操作列 (Sticky Inset-End 防穿透) */}
                    <TableCell
                      className={cn(
                        'sticky inset-e-0 z-10 w-24 text-end',
                        'bg-card relative',
                        'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
                        'group-hover:before:opacity-100 group-data-[state=selected]:before:bg-muted group-data-[state=selected]:before:opacity-100'
                      )}
                    >
                      <div className='flex items-center justify-end gap-1'>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-7 w-7'
                          onClick={() => onEditTask(task)}
                        >
                          <Pencil className='h-3.5 w-3.5' />
                        </Button>
                        <Button
                          variant='ghost'
                          size='icon'
                          className='h-7 w-7 text-destructive hover:text-destructive'
                          onClick={() => onDeleteTask(task)}
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
