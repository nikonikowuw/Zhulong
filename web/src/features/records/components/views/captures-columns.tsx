import { type ColumnDef } from '@tanstack/react-table'
import { type TFunction } from 'i18next'
import { Camera } from 'lucide-react'
import { categoryBadgeClass } from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { cameraLabel, type CameraNameLookup } from '../../data/camera-label'
import { formatRecordTime } from '../../data/format-time'
import { algorithmLabel, type Capture } from '../../data/schema'
import { formatTargetType, targetTypeBadgeClass } from '../../data/target-type'
import { extractVehicleAttributes } from '../../data/vehicle-attributes'
import { MediaImage } from '../common/media-image'
import { VehicleEvidence } from '../vehicles/vehicle-evidence'
import { createSelectionColumn } from './create-selection-column'

export function buildCaptureColumns(
  mode: 'captures' | 'vehicles',
  t: TFunction<'records'>,
  cameraName?: CameraNameLookup
): ColumnDef<Capture>[] {
  const cols: ColumnDef<Capture>[] = [
    createSelectionColumn<Capture>(),
    {
      accessorKey: 'cropPath',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={
            mode === 'vehicles' ? t('table.evidence') : t('table.thumbnail')
          }
        />
      ),
      size: mode === 'vehicles' ? 220 : 90,
      enableSorting: false,
      cell: ({ row }) =>
        mode === 'vehicles' ? (
          <VehicleEvidence capture={row.original} layout='table' />
        ) : (
          <div className='h-10 w-16 overflow-hidden rounded-md border border-border bg-muted/40'>
            <MediaImage
              path={row.original.cropPath || row.original.imagePath}
              alt={formatTargetType(t, row.original.targetType)}
              className='size-full'
              imageClassName='object-contain p-0.5'
              fallbackText={t('card.fallbackCaptureShort')}
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
  ]

  if (mode === 'vehicles') {
    cols.push({
      id: 'plate',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.plate')} />
      ),
      enableSorting: false,
      cell: ({ row }) => {
        const { plateNumber: plateNo, plateColor } = extractVehicleAttributes(
          row.original.attributes
        )
        if (!plateNo) {
          return <span className='text-xs text-muted-foreground'>-</span>
        }
        return (
          <div className='flex items-center gap-1.5'>
            <span
              className={cn(
                'inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-xs font-semibold',
                categoryBadgeClass('blue')
              )}
            >
              {plateNo}
            </span>
            {plateColor && (
              <span className='text-[10px] text-muted-foreground'>
                {plateColor}
              </span>
            )}
          </div>
        )
      },
    })
  }

  if (mode === 'captures') {
    cols.push({
      accessorKey: 'targetType',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('table.type')} />
      ),
      enableSorting: false,
      cell: ({ row }) => (
        <Badge
          variant='outline'
          className={cn(
            'font-medium',
            targetTypeBadgeClass(row.original.targetType)
          )}
        >
          <Camera className='me-1 size-3' />
          {formatTargetType(t, row.original.targetType)}
        </Badge>
      ),
    })
  }

  cols.push(
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
    {
      // 展示 algorithmLabel 而不是裸 algorithmId：算法被卸载后 ID 会变空，
      // 只有写入时快照的名称/版本还能回答「这张抓拍是谁产生的」。
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
    }
  )

  return cols
}
