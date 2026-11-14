import { type ColumnDef } from '@tanstack/react-table'
import { type TFunction } from 'i18next'
import { Bell } from 'lucide-react'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { algorithmLabel, type Alarm } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { createSelectionColumn } from './create-selection-column'

export function buildAlarmColumns(
  t: TFunction<'records'>,
  cameraName?: CameraNameLookup
): ColumnDef<Alarm>[] {
  return [
    createSelectionColumn<Alarm>(),
    {
      accessorKey: 'cropPath',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.thumbnail')} />
      ),
      size: 90,
      enableSorting: false,
      cell: ({ row }) => (
        <div className='size-10 overflow-hidden rounded-md border border-border bg-muted/40'>
          <MediaImage
            path={row.original.cropPath || row.original.imagePath}
            alt={row.original.alarmType}
            className='size-full'
            fallbackText={t('card.fallbackAlarmShort')}
          />
        </div>
      ),
    },
    {
      accessorKey: 'capturedAt',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.time')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <span className='font-mono text-xs'>
          {formatRecordTime(row.original.capturedAt)}
        </span>
      ),
    },
    {
      accessorKey: 'cameraId',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.camera')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <span className='text-xs font-medium'>
          {cameraLabel(row.original.cameraId, cameraName)}
        </span>
      ),
    },
    {
      accessorKey: 'alarmType',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.target')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <Badge
          variant='outline'
          className={cn('gap-1.5 font-medium', statusLevelClass('destructive'))}
        >
          <span
            className={cn(
              'size-1.5 rounded-full',
              statusDotClass('destructive')
            )}
          />
          <Bell className='size-3' />
          {row.original.alarmType}
        </Badge>
      ),
    },
    {
      accessorKey: 'score',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.score')} />
      ),
      enableSorting: false,
      cell: ({ row }) => {
        const score = row.original.score
        return (
          <span className='font-mono text-xs font-semibold'>
            {score > 0 ? `${Math.round(score * 100)}%` : '-'}
          </span>
        )
      },
    },
    {
      accessorKey: 'algorithmId',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.algorithmId')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <span className='font-mono text-xs text-muted-foreground'>
          {algorithmLabel(row.original) || '-'}
        </span>
      ),
    },
  ]
}
