import { type ColumnDef } from '@tanstack/react-table'
import { type TFunction } from 'i18next'
import { UserCheck, UserX } from 'lucide-react'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { type Recognition } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { createSelectionColumn } from './create-selection-column'

export function buildRecognitionColumns(
  t: TFunction<'records'>,
  cameraName?: CameraNameLookup
): ColumnDef<Recognition>[] {
  return [
    createSelectionColumn<Recognition>(),
    {
      accessorKey: 'faceImagePath',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.thumbnail')} />
      ),
      size: 90,
      enableSorting: false,
      cell: ({ row }) => (
        <div className='size-10 overflow-hidden rounded-md border border-border bg-muted/40'>
          <MediaImage
            path={row.original.faceImagePath || row.original.imagePath}
            alt={row.original.personName || t('card.fallbackFaceShort')}
            className='size-full'
            fallbackText={t('card.fallbackFaceShort')}
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
      accessorKey: 'personName',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.target')} />
      ),
      enableSorting: false,
      cell: ({ row }) => {
        const { matched, personName, personId } = row.original
        return (
          <div className='flex items-center gap-2'>
            {matched ? (
              <Badge
                variant='outline'
                className={cn('font-medium', statusLevelClass('active'))}
              >
                <UserCheck className='me-1 size-3' />
                {personName || personId || t('card.matched')}
              </Badge>
            ) : (
              <Badge
                variant='outline'
                className={cn('font-medium', statusLevelClass('warning'))}
              >
                <UserX className='me-1 size-3' />
                {t('card.stranger')}
              </Badge>
            )}
          </div>
        )
      },
    },
    {
      accessorKey: 'similarity',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.score')} />
      ),
      enableSorting: false,
      cell: ({ row }) => {
        const sim = row.original.similarity
        if (!row.original.matched || sim <= 0) {
          return <span className='text-xs text-muted-foreground'>-</span>
        }
        return (
          <span className='font-mono text-xs font-semibold'>
            {Math.round(sim * 100)}%
          </span>
        )
      },
    },
    {
      accessorKey: 'trackId',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.trackId')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <span className='font-mono text-xs text-muted-foreground'>
          {row.original.trackId > 0 ? `#${row.original.trackId}` : '-'}
        </span>
      ),
    },
  ]
}
