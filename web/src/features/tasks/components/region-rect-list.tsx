import { Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { categorySolidClass, categoryTextClass } from '@/lib/category-palette'
import { Button } from '@/components/ui/button'
import {
  MAX_RECTS_PER_CATEGORY,
  REGION_CATEGORY_HUE,
  type RegionCategory,
  type RegionDraft,
  type RegionItem,
  type RegionSelection,
} from '../data/region-config'

export interface RegionRectListProps {
  draft: RegionDraft
  selected: RegionSelection | null
  disabled?: boolean
  onSelect: (selection: RegionSelection) => void
  onDelete: (category: RegionCategory, index: number) => void
}

export function RegionRectList({
  draft,
  selected,
  disabled = false,
  onSelect,
  onDelete,
}: RegionRectListProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const renderItem = (
    item: RegionItem,
    category: RegionCategory,
    idx: number
  ) => {
    const isSelected = selected?.category === category && selected.index === idx
    const hue = REGION_CATEGORY_HUE[category]
    const [x1, y1, x2, y2] = item.rect
    const bcX = ((x1 + x2) / 2).toFixed(3)
    const bcY = y2.toFixed(3)

    return (
      <div
        key={item.id}
        onClick={() => onSelect({ category, index: idx })}
        className={`flex cursor-pointer items-center justify-between rounded-md border p-2 text-xs transition-colors ${
          isSelected
            ? 'border-primary bg-primary/10 shadow-sm'
            : 'border-border/60 hover:border-border hover:bg-muted/40'
        }`}
      >
        <div className='flex flex-col gap-0.5'>
          <div className='flex items-center gap-1.5 font-medium'>
            <span
              className={`h-2 w-2 rounded-full ${categorySolidClass(hue)}`}
            />
            <span>
              {category.toUpperCase()} #{idx + 1}
            </span>
          </div>
          <div className='font-mono text-[11px] text-muted-foreground'>
            [{x1.toFixed(3)}, {y1.toFixed(3)}, {x2.toFixed(3)}, {y2.toFixed(3)}]
          </div>
          <div className='text-[10px] text-muted-foreground/75'>
            {t('region.bottomCenter')}: ({bcX}, {bcY})
          </div>
        </div>

        <Button
          type='button'
          variant='ghost'
          size='icon'
          disabled={disabled}
          className='h-7 w-7 text-muted-foreground hover:text-destructive'
          onClick={(e) => {
            e.stopPropagation()
            onDelete(category, idx)
          }}
          title={t('region.deleteRect')}
        >
          <Trash2 className='h-3.5 w-3.5' />
        </Button>
      </div>
    )
  }

  const renderGroup = (
    category: RegionCategory,
    items: RegionItem[],
    titleKey: string,
    ruleKey: string,
    emptyKey: string
  ) => (
    <div className='space-y-2'>
      <div className='flex items-center justify-between'>
        <span
          className={`font-semibold ${categoryTextClass(REGION_CATEGORY_HUE[category])}`}
        >
          {t(titleKey)} ({items.length}/{MAX_RECTS_PER_CATEGORY})
        </span>
        <span className='text-[10px] text-muted-foreground'>{t(ruleKey)}</span>
      </div>
      {items.length === 0 ? (
        <p className='py-1 text-muted-foreground/60 italic'>{t(emptyKey)}</p>
      ) : (
        <div className='space-y-1.5'>
          {items.map((item, i) => renderItem(item, category, i))}
        </div>
      )}
    </div>
  )

  return (
    <div className='flex flex-col gap-4 text-xs'>
      {renderGroup(
        'roi',
        draft.roi,
        'region.roiTitle',
        'region.roiRule',
        'region.emptyRoi'
      )}
      {renderGroup(
        'mask',
        draft.mask,
        'region.maskTitle',
        'region.maskRule',
        'region.emptyMask'
      )}
    </div>
  )
}
