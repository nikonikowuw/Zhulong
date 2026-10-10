import { Cross2Icon } from '@radix-ui/react-icons'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { DataTableFacetedFilter } from '@/components/data-table/faceted-filter'
import { DataTableViewOptions } from '@/components/data-table/view-options'
import { type AuditLog } from '../data/schema'
import { type AuditDateRange, type TimePresetKey } from '../data/time-presets'
import { AuditDateRangePicker } from './AuditDateRangePicker'

interface AuditToolbarProps {
  table: Table<AuditLog>
  preset?: TimePresetKey
  startTime?: string
  endTime?: string
  onDateRangeChange: (params: AuditDateRange) => void
  actionOptions: { label: string; value: string }[]
  statusOptions: { label: string; value: string }[]
  columnLabels?: Record<string, string>
}

export function AuditToolbar({
  table,
  preset,
  startTime,
  endTime,
  onDateRangeChange,
  actionOptions,
  statusOptions,
  columnLabels,
}: AuditToolbarProps) {
  const { t } = useTranslation('audit')
  const actionColumn = table.getColumn('action')
  const statusColumn = table.getColumn('status')

  const isFiltered =
    table.getState().columnFilters.length > 0 ||
    Boolean(preset || startTime || endTime)

  return (
    <div className='flex items-center justify-between'>
      <div className='flex flex-1 flex-col-reverse items-start gap-y-2 sm:flex-row sm:items-center sm:gap-x-2'>
        <div className='flex flex-wrap items-center gap-2'>
          {actionColumn && (
            <DataTableFacetedFilter
              column={actionColumn}
              title={t('toolbar.actionFilterPlaceholder')}
              options={actionOptions}
              showFacets={false}
            />
          )}

          {statusColumn && (
            <DataTableFacetedFilter
              column={statusColumn}
              title={t('toolbar.statusFilterPlaceholder')}
              options={statusOptions}
              showFacets={false}
            />
          )}

          <AuditDateRangePicker
            preset={preset}
            startTime={startTime}
            endTime={endTime}
            onRangeChange={onDateRangeChange}
          />

          {isFiltered && (
            <Button
              variant='ghost'
              onClick={() => {
                table.resetColumnFilters()
                onDateRangeChange({})
              }}
              className='h-8 px-2 lg:px-3'
            >
              {t('toolbar.reset')}
              <Cross2Icon className='ms-2 h-4 w-4' />
            </Button>
          )}
        </div>
      </div>

      <DataTableViewOptions table={table} columnLabels={columnLabels} />
    </div>
  )
}
