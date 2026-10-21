import { type Row, type Table } from '@tanstack/react-table'
import {
  AlertCircle,
  Eye,
  Image as ImageIcon,
  Plus,
  RefreshCw,
  Users as UsersIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { personsApi } from '../api/persons-api'
import { type Person } from '../data/schema'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'
import { usePersonsContext } from './persons-provider'

interface PersonsGridProps {
  table: Table<Person>
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}

function PersonCard({ row }: { row: Row<Person> }) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow, setPreviewImage } = usePersonsContext()
  const person = row.original

  const primaryFaceId = person.primaryFaceId || person.faces?.[0]?.id
  const avatarUrl = primaryFaceId
    ? personsApi.getFaceImageUrl(person.id, primaryFaceId)
    : undefined

  const faceCount = person.faces ? person.faces.length : 0
  const fallbackChar = person.name
    ? person.name.trim().charAt(0).toUpperCase()
    : '?'

  const handleOpenDetail = () => {
    setCurrentRow(person)
    setOpen('faces')
  }

  const handlePreview = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!avatarUrl) return
    setPreviewImage({
      url: avatarUrl,
      title: `${person.name} (${primaryFaceId})`,
    })
  }

  return (
    <div
      data-testid='person-card'
      onClick={handleOpenDetail}
      className={cn(
        'group relative flex cursor-pointer flex-col justify-between overflow-hidden rounded-xl border bg-card p-4 shadow-2xs transition-all duration-200 select-none hover:border-primary/50 hover:shadow-md',
        row.getIsSelected() &&
          'border-primary bg-primary/5 ring-2 ring-primary/20'
      )}
    >
      {/* 顶部工具行：复选框与行操作菜单 */}
      <div
        className='flex items-center justify-between pb-3'
        onClick={(e) => e.stopPropagation()}
      >
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(value) => row.toggleSelected(!!value)}
          aria-label={`Select ${person.name}`}
          className='translate-y-0.5'
        />
        <PersonsDataTableRowActions row={row} />
      </div>

      {/* 中部人脸展示区 */}
      <div className='flex flex-col items-center gap-3 py-1 text-center'>
        <div className='group/avatar relative h-20 w-20 shrink-0 overflow-hidden rounded-full border-2 border-border/80 shadow-xs'>
          {avatarUrl ? (
            <>
              <img
                src={avatarUrl}
                alt={person.name}
                className='h-full w-full object-cover transition-transform duration-300 group-hover/avatar:scale-105'
              />
              <button
                type='button'
                onClick={handlePreview}
                title={t('imagePreview.enterFullscreen', {
                  defaultValue: '查看大图',
                })}
                className='absolute inset-0 flex cursor-pointer items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover/avatar:opacity-100'
              >
                <Eye className='h-4 w-4 text-white drop-shadow-xs' />
              </button>
            </>
          ) : (
            <Avatar className='h-full w-full'>
              <AvatarFallback className='bg-primary/10 text-xl font-bold text-primary'>
                {fallbackChar}
              </AvatarFallback>
            </Avatar>
          )}
        </div>

        <div className='w-full space-y-1 px-1'>
          <h4
            className='truncate text-sm font-semibold text-foreground'
            title={person.name}
          >
            {person.name}
          </h4>
          <p
            className='truncate font-mono text-xs text-muted-foreground'
            title={person.personId || '-'}
          >
            {person.personId?.trim() ? person.personId : '-'}
          </p>
        </div>
      </div>

      {/* 底部信息标签与快捷入口 */}
      <div className='mt-4 flex items-center justify-between border-t border-border/50 pt-3'>
        {faceCount > 0 ? (
          <Badge
            variant='secondary'
            className='gap-1 px-2 py-0.5 text-[11px] font-normal text-muted-foreground'
          >
            <ImageIcon className='h-3 w-3' />
            <span>{t('fields.faceCountUnit', { count: faceCount })}</span>
          </Badge>
        ) : (
          <Badge
            variant='outline'
            className='border-amber-400/60 bg-amber-500/10 text-[10px] text-amber-700 dark:border-amber-500/40 dark:text-amber-400'
          >
            {t('status.unregistered')}
          </Badge>
        )}

        <Button
          type='button'
          size='sm'
          variant='ghost'
          onClick={(e) => {
            e.stopPropagation()
            handleOpenDetail()
          }}
          className='h-7 px-2 text-xs text-primary hover:bg-primary/10'
        >
          <span>{t('actions.manageFaces')}</span>
        </Button>
      </div>
    </div>
  )
}

export function PersonsGrid({
  table,
  isLoading,
  isError,
  onRetry,
}: PersonsGridProps) {
  const { t } = useTranslation('persons')
  const { setOpen } = usePersonsContext()

  const rows = table.getRowModel().rows
  const isFiltered =
    table.getState().columnFilters.length > 0 ||
    Boolean(table.getState().globalFilter)

  if (isLoading) {
    return (
      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4'>
        {Array.from({ length: 8 }).map((_, i) => (
          <div
            key={`grid-skeleton-${i}`}
            className='flex h-60 flex-col items-center justify-between rounded-xl border border-border bg-card p-4'
          >
            <div className='flex w-full justify-between'>
              <Skeleton className='h-4 w-4 rounded-xs' />
              <Skeleton className='h-7 w-7 rounded-md' />
            </div>
            <Skeleton className='h-20 w-20 rounded-full' />
            <div className='flex w-full flex-col items-center space-y-2'>
              <Skeleton className='h-4 w-28' />
              <Skeleton className='h-3 w-20' />
            </div>
            <div className='flex w-full justify-between border-t border-border/50 pt-2'>
              <Skeleton className='h-5 w-16 rounded-md' />
              <Skeleton className='h-5 w-14 rounded-md' />
            </div>
          </div>
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div className='flex flex-col items-center justify-center rounded-xl border border-border bg-card py-16 text-destructive'>
        <AlertCircle className='h-8 w-8' />
        <span className='mt-2 font-medium'>{t('table.loadFailed')}</span>
        <Button variant='outline' size='sm' onClick={onRetry} className='mt-4'>
          <RefreshCw className='me-1 h-4 w-4' />
          {t('actions.retry')}
        </Button>
      </div>
    )
  }

  if (rows.length === 0) {
    return (
      <div className='flex flex-col items-center justify-center rounded-xl border border-border bg-card py-16 text-center'>
        <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
          <UsersIcon className='h-6 w-6 text-muted-foreground' />
        </div>
        <span className='mt-3 text-base font-semibold text-foreground'>
          {isFiltered ? t('table.filterEmpty') : t('table.empty')}
        </span>
        <p className='mt-1 max-w-sm text-sm text-muted-foreground'>
          {isFiltered ? t('table.filterEmptyDesc') : t('table.emptyDesc')}
        </p>
        {isFiltered ? (
          <Button
            variant='outline'
            size='sm'
            onClick={() => {
              table.resetGlobalFilter()
              table.resetColumnFilters()
            }}
            className='mt-4'
          >
            {t('table.resetFilter')}
          </Button>
        ) : (
          <Button size='sm' onClick={() => setOpen('create')} className='mt-4'>
            <Plus className='me-1 h-4 w-4' />
            {t('actions.create')}
          </Button>
        )}
      </div>
    )
  }

  return (
    <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4'>
      {rows.map((row) => (
        <PersonCard key={row.original.id} row={row} />
      ))}
    </div>
  )
}
