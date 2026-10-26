import { type ColumnDef } from '@tanstack/react-table'
import { type TFunction } from 'i18next'
import { KeyRound, Layers, MoreHorizontal, Trash2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { DataTableColumnHeader } from '@/components/data-table'
import { type Gb28181Device } from '../data/schema'
import {
  EMPTY_PLACEHOLDER,
  deviceStateLevel,
  formatTimestamp,
} from '../utils/format'
import { Gb28181StatusBadge } from './gb28181-status-badge'

interface DeviceColumnOptions {
  t: TFunction
  /** 时间戳按当前界面语言格式化，切换语言要重建列。 */
  language: string
  onEdit: (device: Gb28181Device) => void
  onViewChannels: (device: Gb28181Device) => void
  onDelete: (device: Gb28181Device) => void
}

/**
 * 设备 allowlist 的列定义。
 *
 * 与 `features/cameras` / `features/persons` 同构：列定义拆到 `*-columns.tsx`，
 * 表格组件只负责分页、筛选与三态（规范 §4.1 列表页骨架）。
 */
export function createGb28181DeviceColumns({
  t,
  language,
  onEdit,
  onViewChannels,
  onDelete,
}: DeviceColumnOptions): ColumnDef<Gb28181Device>[] {
  return [
    {
      accessorKey: 'deviceId',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.deviceId')}
        />
      ),
      cell: ({ row }) => (
        <span className='font-mono text-xs text-foreground'>
          {row.original.deviceId}
        </span>
      ),
    },
    {
      accessorKey: 'name',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.name')}
        />
      ),
      cell: ({ row }) => (
        <span className='text-sm'>
          {row.original.name || EMPTY_PLACEHOLDER}
        </span>
      ),
    },
    {
      accessorKey: 'transport',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.transport')}
        />
      ),
      cell: ({ row }) => (
        <span className='font-mono text-xs uppercase'>
          {row.original.transport}
        </span>
      ),
      enableSorting: false,
    },
    {
      accessorKey: 'state',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.state')}
        />
      ),
      cell: ({ row }) => (
        <Gb28181StatusBadge
          level={deviceStateLevel(row.original.state)}
          label={t(`deviceState.${row.original.state}`, {
            defaultValue: row.original.state,
          })}
        />
      ),
      // 多选分面筛选（Faceted Filter）按数组包含匹配，避免默认 includesString 将多选数组拼成字符串导致漏配。
      filterFn: (row, id, value: unknown) => {
        if (!Array.isArray(value) || value.length === 0) return true
        return value.includes(String(row.getValue(id)))
      },
    },
    {
      accessorKey: 'remoteAddr',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.remoteAddr')}
        />
      ),
      cell: ({ row }) => (
        <span className='font-mono text-xs text-muted-foreground'>
          {row.original.remoteAddr || EMPTY_PLACEHOLDER}
        </span>
      ),
      enableSorting: false,
    },
    {
      accessorKey: 'expiresSeconds',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.expires')}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs text-muted-foreground'>
          {t('devices.expiresValue', {
            seconds: row.original.expiresSeconds,
          })}
        </span>
      ),
      enableSorting: false,
    },
    {
      accessorKey: 'lastKeepaliveAt',
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={t('devices.columns.lastKeepalive')}
        />
      ),
      cell: ({ row }) => (
        <span className='text-xs text-muted-foreground'>
          {formatTimestamp(row.original.lastKeepaliveAt, language)}
        </span>
      ),
      enableSorting: false,
    },
    {
      id: 'actions',
      header: () => (
        <span className='sr-only'>{t('devices.columns.actions')}</span>
      ),
      cell: ({ row }) => (
        <div className='flex justify-end'>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant='ghost'
                size='icon'
                className='size-8'
                aria-label={t('devices.columns.actions')}
                title={t('devices.columns.actions')}
              >
                <MoreHorizontal className='size-4' />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuItem onClick={() => onViewChannels(row.original)}>
                <Layers />
                {t('devices.channelsCount', {
                  count: row.original.channelCount ?? 0,
                })}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onEdit(row.original)}>
                <KeyRound />
                {t('devices.editDevice')}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant='destructive'
                onClick={() => onDelete(row.original)}
              >
                <Trash2 />
                {t('devices.deleteDevice')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ),
      enableSorting: false,
      enableHiding: false,
      meta: {
        // 悬浮高亮用伪元素叠加，底层保持实色，横向滚动时不会透出文字。
        className: cn(
          'relative sticky inset-e-0 z-10 w-12 bg-card text-end',
          'before:pointer-events-none before:absolute before:inset-0 before:bg-muted/50 before:opacity-0 before:transition-opacity',
          'group-hover:before:opacity-100'
        ),
      },
    },
  ]
}
