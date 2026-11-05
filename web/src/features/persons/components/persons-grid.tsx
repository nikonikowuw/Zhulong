import { memo, useState } from 'react'
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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { personsApi } from '../api/persons-api'
import { type Person } from '../data/schema'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'
import { usePersonsActions } from './persons-provider'

interface PersonsGridProps {
  table: Table<Person>
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}

const PersonCard = memo(function PersonCard({ row }: { row: Row<Person> }) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow, setPreviewImage } = usePersonsActions()
  const [imgError, setImgError] = useState(false)
  const person = row.original

  const primaryFaceId = person.primaryFaceId || person.faces?.[0]?.id
  const avatarUrl = primaryFaceId
    ? personsApi.getFaceImageUrl(person.id, primaryFaceId)
    : undefined

  const faces = person.faces ?? []
  const faceCount = faces.length
  const fallbackChar = person.name
    ? person.name.trim().charAt(0).toUpperCase()
    : '?'

  const handleOpenDetail = () => {
    setCurrentRow(person)
    setOpen('faces')
  }

  const handlePreview = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!avatarUrl || imgError) return
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
        'group relative flex cursor-pointer flex-col overflow-hidden rounded-xl border bg-card shadow-2xs transition-all duration-300 select-none hover:-translate-y-1 hover:border-primary/50 hover:shadow-lg',
        row.getIsSelected() &&
          'border-primary bg-primary/5 ring-2 ring-primary/30'
      )}
    >
      {/* 顶部大封面相框区 */}
      <div className='relative aspect-square w-full overflow-hidden bg-muted/30'>
        {avatarUrl && !imgError ? (
          <>
            <img
              src={avatarUrl}
              alt={person.name}
              onError={() => setImgError(true)}
              className='h-full w-full object-cover transition-transform duration-500 group-hover:scale-105'
              loading='lazy'
              decoding='async'
            />
            {/* 渐变遮罩与快捷放大 */}
            <div className='absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-black/20 opacity-30 transition-opacity duration-300 group-hover:opacity-60' />
            <button
              type='button'
              onClick={handlePreview}
              title={t('imagePreview.enterFullscreen', {
                defaultValue: '查看大图',
              })}
              className='absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-200 group-hover:opacity-100'
            >
              <div className='flex h-10 w-10 items-center justify-center rounded-full bg-black/60 text-white shadow-md backdrop-blur-xs transition-transform hover:scale-110'>
                <Eye className='h-5 w-5 drop-shadow-xs' />
              </div>
            </button>
          </>
        ) : (
          /* 无人脸或加载失败时的精致空态占位 */
          <div className='flex h-full w-full flex-col items-center justify-center gap-2.5 bg-gradient-to-b from-muted/20 to-muted/60 p-4 text-muted-foreground'>
            <div className='flex h-16 w-16 items-center justify-center rounded-2xl border border-dashed border-border/80 bg-background/60 shadow-inner'>
              <span className='text-2xl font-bold text-primary/70'>
                {fallbackChar}
              </span>
            </div>
            <span className='text-xs font-medium text-muted-foreground/70'>
              {t('status.unregistered')}
            </span>
          </div>
        )}

        {/* 悬浮工具层：毛玻璃复选框 (左上) */}
        <div
          className='absolute start-2.5 top-2.5 z-10'
          onClick={(e) => e.stopPropagation()}
        >
          <div className='flex h-7 w-7 items-center justify-center rounded-lg border border-border/40 bg-background/80 shadow-xs backdrop-blur-md transition-colors hover:bg-background'>
            <Checkbox
              checked={row.getIsSelected()}
              onCheckedChange={(value) => row.toggleSelected(!!value)}
              aria-label={`Select ${person.name}`}
            />
          </div>
        </div>

        {/* 悬浮工具层：操作菜单 (右上) */}
        <div
          className='absolute end-2.5 top-2.5 z-10'
          onClick={(e) => e.stopPropagation()}
        >
          <div className='flex items-center justify-center rounded-lg border border-border/40 bg-background/80 shadow-xs backdrop-blur-md transition-colors hover:bg-background'>
            <PersonsDataTableRowActions row={row} />
          </div>
        </div>

        {/* 照片角标：底库人脸数 */}
        {faceCount > 0 && (
          <div className='pointer-events-none absolute start-2.5 bottom-2.5 z-10'>
            <Badge
              variant='secondary'
              className='gap-1 border-white/10 bg-black/60 px-2 py-0.5 text-[11px] font-medium text-white shadow-xs backdrop-blur-md'
            >
              <ImageIcon className='h-3 w-3' />
              <span>{t('fields.faceCountUnit', { count: faceCount })}</span>
            </Badge>
          </div>
        )}
      </div>

      {/* 下部信息与快捷动作栏 */}
      <div className='flex flex-1 flex-col justify-between p-3.5'>
        <div className='space-y-1'>
          <h4
            className='truncate text-sm font-semibold text-foreground transition-colors group-hover:text-primary'
            title={person.name}
          >
            {person.name}
          </h4>
          <p
            className='truncate font-mono text-xs text-muted-foreground'
            title={person.personId?.trim() ? person.personId : '-'}
          >
            {person.personId?.trim() ? person.personId : '-'}
          </p>
        </div>

        {/* 底部底库头像小堆叠 (Face Stack) 与快捷操作 */}
        <div className='mt-3.5 flex items-center justify-between border-t border-border/50 pt-2.5'>
          {faceCount > 0 ? (
            <div className='flex items-center -space-x-1.5 overflow-hidden py-0.5'>
              {faces.slice(0, 3).map((face) => (
                <img
                  key={face.id}
                  src={personsApi.getFaceImageUrl(person.id, face.id)}
                  alt=''
                  className='h-5 w-5 rounded-full border-2 border-card object-cover shadow-2xs'
                  loading='lazy'
                  decoding='async'
                />
              ))}
              {faceCount > 3 && (
                <span className='flex h-5 items-center justify-center rounded-full bg-muted px-1.5 font-mono text-[9px] font-medium text-muted-foreground ring-1 ring-border/50'>
                  +{faceCount - 3}
                </span>
              )}
            </div>
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
            className='h-7 px-2 text-xs font-medium text-primary hover:bg-primary/10'
          >
            <span>{t('actions.manageFaces')}</span>
          </Button>
        </div>
      </div>
    </div>
  )
})

export function PersonsGrid({
  table,
  isLoading,
  isError,
  onRetry,
}: PersonsGridProps) {
  const { t } = useTranslation('persons')
  const { setOpen } = usePersonsActions()

  const rows = table.getRowModel().rows
  const isFiltered =
    table.getState().columnFilters.length > 0 ||
    Boolean(table.getState().globalFilter)

  if (isLoading) {
    return (
      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6'>
        {Array.from({ length: 8 }).map((_, i) => (
          <div
            key={`grid-skeleton-${i}`}
            className='flex flex-col overflow-hidden rounded-xl border border-border bg-card'
          >
            <Skeleton className='aspect-square w-full rounded-none' />
            <div className='space-y-3 p-3.5'>
              <div className='space-y-1.5'>
                <Skeleton className='h-4 w-28' />
                <Skeleton className='h-3 w-20' />
              </div>
              <div className='flex items-center justify-between border-t border-border/50 pt-2.5'>
                <Skeleton className='h-5 w-16 rounded-md' />
                <Skeleton className='h-7 w-16 rounded-md' />
              </div>
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
    <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6'>
      {rows.map((row) => (
        <PersonCard key={row.original.id} row={row} />
      ))}
    </div>
  )
}
