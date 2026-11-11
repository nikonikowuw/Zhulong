import { memo } from 'react'
import { type Column, type Row, type Table } from '@tanstack/react-table'
import { Eye, Image as ImageIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { CATEGORY_BADGE_CLASS } from '@/lib/category-palette'
import { getGalleryImageUrl } from '@/lib/media-url'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { DataTableColumnHeader } from '@/components/data-table'
import { type Person } from '../data/schema'
import { initialOf, primaryFaceIdOf } from '../persons-helpers'
import { usePersonsActions } from './persons-provider'

interface PersonColumnHeaderProps<TValue> {
  column: Column<Person, TValue>
  titleKey: string
}

/**
 * 列头。`TValue` 由调用点的 `Column<Person, X>` 推出 —— 曾经这里写成
 * `column: any` 并配一条 `eslint-disable`，代价是列头组件彻底失去类型检查。
 */
export function PersonColumnHeader<TValue>({
  column,
  titleKey,
}: PersonColumnHeaderProps<TValue>) {
  const { t } = useTranslation('persons')
  return <DataTableColumnHeader column={column} title={t(titleKey)} />
}

/** 表头全选勾选框（本页）。放在这里而不是列定义文件：列定义模块只应导出数据。 */
export function PersonSelectAllCheckbox({ table }: { table: Table<Person> }) {
  const { t } = useTranslation('persons')
  return (
    <Checkbox
      checked={
        table.getIsAllPageRowsSelected() ||
        (table.getIsSomePageRowsSelected() && 'indeterminate')
      }
      onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
      aria-label={t('table.selectAll')}
      className='translate-y-0.5'
    />
  )
}

/** 行选择勾选框。 */
export function PersonRowSelectCheckbox({ row }: { row: Row<Person> }) {
  const { t } = useTranslation('persons')
  return (
    <Checkbox
      checked={row.getIsSelected()}
      onCheckedChange={(value) => row.toggleSelected(!!value)}
      aria-label={t('table.selectRow')}
      className='translate-y-0.5'
    />
  )
}

/** 操作列的表头（视觉隐藏，保留给屏读器）。 */
export function PersonActionsHeader() {
  const { t } = useTranslation('persons')
  return <span className='sr-only'>{t('table.columns.actions')}</span>
}

export const PersonAvatarCell = memo(function PersonAvatarCell({
  person,
}: {
  person: Person
}) {
  const { t } = useTranslation('persons')
  const { setPreviewImage } = usePersonsActions()
  const primaryFaceId = primaryFaceIdOf(person)
  const avatarUrl = getGalleryImageUrl(person.id, primaryFaceId)
  const fallbackChar = initialOf(person.name)

  const handlePreview = () => {
    if (!avatarUrl) return
    setPreviewImage({
      url: avatarUrl,
      title: `${person.name} (${primaryFaceId})`,
    })
  }

  if (!avatarUrl) {
    return (
      <div className='flex items-center justify-center'>
        <Avatar className='h-9 w-9 rounded-full border border-border bg-muted'>
          <AvatarFallback className='bg-primary/10 text-xs font-semibold text-primary'>
            {fallbackChar}
          </AvatarFallback>
        </Avatar>
      </div>
    )
  }

  return (
    <div className='flex items-center justify-center'>
      <button
        type='button'
        onClick={handlePreview}
        title={person.name}
        aria-label={t('table.previewAvatarOf', { name: person.name })}
        className='group relative flex shrink-0 cursor-pointer rounded-full focus:ring-2 focus:ring-primary focus:ring-offset-2 focus:outline-hidden'
      >
        <Avatar className='h-9 w-9 rounded-full border border-border shadow-xs transition-transform duration-200 group-hover:scale-110'>
          <AvatarImage
            src={avatarUrl}
            alt={person.name}
            className='object-cover'
            loading='lazy'
            decoding='async'
          />
          <AvatarFallback className='bg-primary/10 text-xs font-semibold text-primary'>
            {fallbackChar}
          </AvatarFallback>
        </Avatar>
        <div className='pointer-events-none absolute inset-0 flex items-center justify-center rounded-full bg-black/35 opacity-0 transition-opacity group-hover:opacity-100'>
          <Eye className='h-3.5 w-3.5 text-white drop-shadow-xs' />
        </div>
      </button>
    </div>
  )
})

export const PersonNameCell = memo(function PersonNameCell({
  person,
}: {
  person: Person
}) {
  return (
    <span className='truncate font-medium text-foreground'>{person.name}</span>
  )
})

/**
 * 来源列：`LOCAL` / `API` 是**分类**而不是状态，因此走分类色板的
 * 第 1 槽位（blue），且不带状态圆点 —— 状态色相（teal/amber/destructive/
 * neutral）是 §1.3.3 专属，私造 `bg-blue-500` 圆点会把它读成"在线"。
 */
export const PersonSourceCell = memo(function PersonSourceCell({
  person,
}: {
  person: Person
}) {
  const { t } = useTranslation('persons')
  if (person.source === 'API') {
    return (
      <Badge
        variant='outline'
        className={cn('text-[11px] font-normal', CATEGORY_BADGE_CLASS.blue)}
        title={person.sourceName ? `API: ${person.sourceName}` : 'API'}
      >
        {person.sourceName || t('table.columns.sourceAPI')}
      </Badge>
    )
  }
  return (
    <Badge
      variant='secondary'
      className='text-[11px] font-normal text-muted-foreground'
    >
      {t('table.columns.sourceLocal')}
    </Badge>
  )
})

/**
 * 人脸底库列。
 *
 * 计数只出现一次：曾经按钮里写 `"0 张"` 再叠一个 `"待采像"` 徽章，
 * `"1 张"` 再叠一个 `"1"` —— 同一数字在一格里画了两遍。
 */
export const PersonsFacesCell = memo(function PersonsFacesCell({
  person,
}: {
  person: Person
}) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow } = usePersonsActions()
  const faceCount = person.faces?.length ?? 0

  const handleOpenFaces = () => {
    setCurrentRow(person)
    setOpen('faces')
  }

  return (
    <Button
      variant='outline'
      size='sm'
      onClick={handleOpenFaces}
      aria-label={t('table.manageFacesOf', { name: person.name })}
      className={cn(
        'h-7 gap-1.5 px-2 text-xs font-normal hover:bg-muted',
        // 「待采像」是 §1.3.3 的 warning 级别，色串必须走 palette：
        // 就地手写 `border-amber-500/30 … dark:text-amber-400` 缺 `dark:border`
        // /`dark:bg` 是规范点名的漂移形态。
        faceCount === 0 && statusLevelClass('warning')
      )}
    >
      <ImageIcon className='h-3.5 w-3.5 text-muted-foreground' />
      <span>
        {faceCount > 0
          ? t('fields.faceCountUnit', { count: faceCount })
          : t('table.missingFaceBadge')}
      </span>
    </Button>
  )
})
