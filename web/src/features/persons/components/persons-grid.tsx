import { memo, useState } from 'react'
import { type Table } from '@tanstack/react-table'
import { Eye, Image as ImageIcon, Plus, Users as UsersIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getGalleryImageUrl } from '@/lib/media-url'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { type Person } from '../data/schema'
import { initialOf, primaryFaceIdOf } from '../persons-helpers'
import { PersonsDataTableRowActions } from './persons-data-table-row-actions'
import { usePersonsActions } from './persons-provider'

interface PersonsGridProps {
  table: Table<Person>
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  /** 清空本地筛选；由 `PersonsTable` 注入，避免空态里直接改 table 实例。 */
  onResetFilters: () => void
  isFiltered: boolean
}

/**
 * 画廊卡片。
 *
 * props 必须是**原始值**而不是 `Row<Person>`：react-table 会原地改写 row 对象
 * （`row.toggleSelected` 只更新内部状态，引用不变），`memo` 的浅比较会把选择
 * 变化整个吞掉。曾经的写法用 `row.getIsSelected()` 托管给 `memo`，结果是
 * checkbox 永远渲染成未选中、且因为受控值恒为 `false`，第二次点击上报的仍然
 * 是 `true` —— 用户在画廊里根本取消不了勾选。
 */
const PersonCard = memo(function PersonCard({
  person,
  isSelected,
  onToggleSelected,
}: {
  person: Person
  isSelected: boolean
  onToggleSelected: (selected: boolean) => void
}) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow, setPreviewImage } = usePersonsActions()
  const [imgError, setImgError] = useState(false)

  const primaryFaceId = primaryFaceIdOf(person)
  const avatarUrl = getGalleryImageUrl(person.id, primaryFaceId)
  const faces = person.faces ?? []
  const faceCount = faces.length
  const fallbackChar = initialOf(person.name)

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
      data-state={isSelected ? 'selected' : undefined}
      className={cn(
        // `content-visibility: auto` + 估算高度：上百张卡片时浏览器只渲染视口
        // 附近的那些，滚动与筛选都明显更跟手。卡片高度≈封面(方图)+信息区。
        'group relative flex flex-col overflow-hidden rounded-xl border bg-card shadow-2xs transition-all duration-300 select-none [contain-intrinsic-size:auto_320px] [content-visibility:auto] hover:-translate-y-1 hover:border-primary/50 hover:shadow-lg',
        isSelected && 'border-primary bg-primary/5 ring-2 ring-primary/30'
      )}
    >
      {/* 封面区点击打开详情。键盘用户走标题按钮或「人脸管理」按钮 —— 不给外层
          div 加 tabIndex，否则会和内部的 checkbox / 按钮抢焦点。 */}
      <div className='relative aspect-square w-full overflow-hidden bg-muted/30'>
        {/* 整片封面是「打开人脸管理」的点击目标（键盘可达），放大镜另作按钮
            叠在上面 —— 曾经的实现把整片封面都做成预览按钮，于是卡片根本没有
            「点一下进详情」的路径。 */}
        <button
          type='button'
          onClick={handleOpenDetail}
          aria-label={t('actions.manageFaces')}
          className='absolute inset-0 h-full w-full cursor-pointer focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-hidden'
        >
          {avatarUrl && !imgError ? (
            <img
              src={avatarUrl}
              alt={person.name}
              onError={() => setImgError(true)}
              className='h-full w-full object-cover transition-transform duration-500 group-hover:scale-105'
              loading='lazy'
              decoding='async'
            />
          ) : (
            <span className='flex h-full w-full flex-col items-center justify-center gap-2.5 bg-gradient-to-b from-muted/20 to-muted/60 p-4 text-muted-foreground'>
              <span className='flex h-16 w-16 items-center justify-center rounded-2xl border border-dashed border-border/80 bg-background/60 shadow-inner'>
                <span className='text-2xl font-bold text-primary/70'>
                  {fallbackChar}
                </span>
              </span>
              <span className='text-xs font-medium text-muted-foreground/70'>
                {t('status.unregistered')}
              </span>
            </span>
          )}
        </button>

        {avatarUrl && !imgError && (
          <>
            <div className='pointer-events-none absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-black/20 opacity-30 transition-opacity duration-300 group-hover:opacity-60' />
            <div className='pointer-events-none absolute inset-x-0 top-1/2 flex justify-center opacity-0 transition-opacity duration-200 group-hover:opacity-100'>
              <button
                type='button'
                onClick={handlePreview}
                aria-label={t('imagePreview.enterFullscreen')}
                title={t('imagePreview.enterFullscreen')}
                className='pointer-events-auto flex h-10 w-10 cursor-zoom-in items-center justify-center rounded-full bg-black/60 text-white shadow-md backdrop-blur-xs transition-transform hover:scale-110 focus-visible:opacity-100'
              >
                <Eye className='h-5 w-5 drop-shadow-xs' />
              </button>
            </div>
          </>
        )}

        {/* 悬浮工具层：毛玻璃复选框 (左上) */}
        <div className='absolute start-2.5 top-2.5 z-10'>
          <div className='flex h-7 w-7 items-center justify-center rounded-lg border border-border/40 bg-background/80 shadow-xs backdrop-blur-md transition-colors hover:bg-background'>
            <Checkbox
              checked={isSelected}
              onCheckedChange={(value) => onToggleSelected(!!value)}
              aria-label={t('table.selectPerson', { name: person.name })}
            />
          </div>
        </div>

        {/* 悬浮工具层：操作菜单 (右上) */}
        <div className='absolute end-2.5 top-2.5 z-10'>
          <div className='flex items-center justify-center rounded-lg border border-border/40 bg-background/80 shadow-xs backdrop-blur-md transition-colors hover:bg-background'>
            <PersonsDataTableRowActions person={person} />
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
          <h4 className='truncate'>
            <button
              type='button'
              onClick={handleOpenDetail}
              title={person.name}
              className='max-w-full cursor-pointer truncate text-start text-sm font-semibold text-foreground transition-colors group-hover:text-primary focus-visible:underline focus-visible:outline-hidden'
            >
              {person.name}
            </button>
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
                  src={getGalleryImageUrl(person.id, face.id)}
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
              className={cn(
                'text-[10px] font-normal',
                statusLevelClass('warning')
              )}
            >
              {t('status.unregistered')}
            </Badge>
          )}

          <Button
            type='button'
            size='sm'
            variant='ghost'
            onClick={handleOpenDetail}
            className='h-7 px-2 text-xs font-medium text-primary hover:bg-primary/10'
          >
            {t('actions.manageFaces')}
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
  isFiltered,
  onResetFilters,
}: PersonsGridProps) {
  const { t } = useTranslation('persons')
  const { setOpen } = usePersonsActions()

  const rows = table.getRowModel().rows

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
        <UsersIcon className='h-8 w-8 opacity-40' />
        <span className='mt-2 font-medium'>{t('table.loadFailed')}</span>
        <Button variant='outline' size='sm' onClick={onRetry} className='mt-4'>
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
            onClick={onResetFilters}
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
        <PersonCard
          key={row.original.id}
          person={row.original}
          isSelected={row.getIsSelected()}
          onToggleSelected={row.toggleSelected}
        />
      ))}
    </div>
  )
}
