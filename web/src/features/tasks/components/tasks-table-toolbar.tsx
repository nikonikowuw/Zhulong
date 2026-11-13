import { type Table as TanstackTable } from '@tanstack/react-table'
import { ChevronDown, ChevronUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { DataTableToolbar } from '@/components/data-table'
import { type ChannelTaskGroup } from '../data/schema'

export interface FacetedOption {
  label: string
  value: string
}

export interface TasksTableToolbarProps {
  table: TanstackTable<ChannelTaskGroup>
  /** 明细视图下可展开的通道数量：为 0 时不渲染「展开全部」。 */
  configuredCount: number
  cameraOptions: FacetedOption[]
  algorithmOptions: FacetedOption[]
  statusOptions: FacetedOption[]
}

/** 列表工具条：搜索、分面筛选与展开/折叠全部。 */
export function TasksTableToolbar({
  table,
  configuredCount,
  cameraOptions,
  algorithmOptions,
  statusOptions,
}: TasksTableToolbarProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  return (
    <div className='flex flex-wrap items-center justify-between gap-2'>
      <div className='flex-1'>
        <DataTableToolbar
          table={table}
          searchPlaceholder={t(
            'searchPlaceholder',
            '搜索任务 ID、摄像头或算法名称...'
          )}
          filters={[
            {
              columnId: 'camera',
              title: t('table.columns.camera', '关联摄像头'),
              options: cameraOptions,
              showFacets: false,
            },
            {
              columnId: 'algorithm',
              title: t('table.columns.algorithm', '绑定算法包'),
              options: algorithmOptions,
              showFacets: false,
            },
            {
              columnId: 'status',
              title: t('table.columns.status', '运行状态'),
              options: statusOptions,
              showFacets: false,
            },
          ]}
          columnLabels={{
            camera: t('table.columns.camera', '关联摄像头'),
            status: t('table.columns.status', '通道状态'),
            algorithm: t('table.columns.algorithm', '已配算法'),
            computePoints: t('table.columns.computePoints', '总算力开销'),
            updatedAt: t('table.columns.updatedAt', '更新时间'),
          }}
        />
      </div>

      <div className='flex items-center gap-2'>
        {configuredCount > 0 && (
          <Button
            variant='outline'
            size='sm'
            className='h-8 gap-1.5 text-xs'
            onClick={() => table.toggleAllRowsExpanded()}
          >
            {table.getIsAllRowsExpanded() ? (
              <>
                <ChevronUp className='size-3.5' />
                <span>{t('table.collapseAll', '折叠全部')}</span>
              </>
            ) : (
              <>
                <ChevronDown className='size-3.5' />
                <span>{t('table.expandAll', '展开全部')}</span>
              </>
            )}
          </Button>
        )}
      </div>
    </div>
  )
}
