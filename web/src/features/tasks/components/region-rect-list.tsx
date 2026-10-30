import { Info, ShieldAlert, Sparkles, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  categoryBadgeClass,
  categorySolidClass,
  categoryTextClass,
} from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
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

const GROUP_META: Record<
  RegionCategory,
  { titleKey: string; ruleKey: string; emptyKey: string; descKey: string }
> = {
  roi: {
    titleKey: 'region.roiTitle',
    ruleKey: 'region.roiRule',
    emptyKey: 'region.emptyRoi',
    descKey: 'region.roiDesc',
  },
  mask: {
    titleKey: 'region.maskTitle',
    ruleKey: 'region.maskRule',
    emptyKey: 'region.emptyMask',
    descKey: 'region.maskDesc',
  },
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
    const bottomCenterX = ((x1 + x2) / 2).toFixed(3)
    const bottomCenterY = y2.toFixed(3)
    const areaPercent = ((x2 - x1) * (y2 - y1) * 100).toFixed(1)

    return (
      <div
        key={item.id}
        role='button'
        tabIndex={0}
        onClick={() => onSelect({ category, index: idx })}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            onSelect({ category, index: idx })
          }
        }}
        className={cn(
          'group relative flex cursor-pointer items-start justify-between rounded-lg border p-2.5 text-xs transition-all',
          isSelected
            ? cn(categoryBadgeClass(hue), 'shadow-xs ring-1 ring-current/30')
            : 'border-border bg-card hover:border-border/80 hover:bg-muted/40'
        )}
      >
        <div className='flex min-w-0 flex-1 flex-col gap-1.5'>
          <div className='flex items-center gap-1.5'>
            {/* 色块（不是状态圆点）：与画布矩形的分类色一一对应 */}
            <span
              aria-hidden
              className={cn(
                'size-2 shrink-0 rounded-[3px]',
                categorySolidClass(hue)
              )}
            />
            <span className='font-semibold text-foreground'>
              {category.toUpperCase()} #{idx + 1}
            </span>
            <Badge
              variant='outline'
              className={cn(
                'h-4.5 px-1.5 text-[10px] font-normal',
                categoryBadgeClass(hue)
              )}
            >
              {category === 'roi'
                ? t('region.kindRoi', '识别区')
                : t('region.kindMask', '屏蔽区')}
            </Badge>
          </div>

          <div className='flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground'>
            <span className='font-mono'>
              {t('region.coverage', '覆盖画面 ~{{percent}}%', {
                percent: areaPercent,
              })}
            </span>
            <span className='text-border'>•</span>
            <span className='font-mono text-muted-foreground/80'>
              {t('region.bottomCenter')}: ({bottomCenterX}, {bottomCenterY})
            </span>
          </div>
        </div>

        <div className='ms-2 flex shrink-0 items-center gap-1'>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            disabled={disabled}
            className='size-7 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive'
            onClick={(event) => {
              event.stopPropagation()
              onDelete(category, idx)
            }}
            title={t('region.deleteRect', '删除区域')}
            aria-label={t('region.deleteRect', '删除区域')}
          >
            <Trash2 className='size-3.5' />
          </Button>
        </div>
      </div>
    )
  }

  const renderGroup = (category: RegionCategory, items: RegionItem[]) => {
    const hue = REGION_CATEGORY_HUE[category]
    const meta = GROUP_META[category]
    const Icon = category === 'roi' ? Sparkles : ShieldAlert

    return (
      <div className='space-y-2'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-1.5'>
            <Icon className={cn('size-3.5', categoryTextClass(hue))} />
            <span className={cn('font-semibold', categoryTextClass(hue))}>
              {t(meta.titleKey)} ({items.length}/{MAX_RECTS_PER_CATEGORY})
            </span>
          </div>
          <span className='font-mono text-[10px] text-muted-foreground'>
            {t(meta.ruleKey)}
          </span>
        </div>

        <p className='text-[11px] leading-normal text-muted-foreground/80'>
          {t(meta.descKey)}
        </p>

        {items.length === 0 ? (
          <div className='flex items-center justify-center rounded-lg border border-dashed border-border/80 bg-muted/20 py-3 text-center'>
            <p className='text-[11px] text-muted-foreground/70 italic'>
              {t(meta.emptyKey)}
            </p>
          </div>
        ) : (
          <div className='space-y-1.5'>
            {items.map((item, index) => renderItem(item, category, index))}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className='flex flex-col gap-5 text-xs'>
      {renderGroup('roi', draft.roi)}
      {renderGroup('mask', draft.mask)}

      <div className='rounded-lg border border-border/60 bg-muted/30 p-3 text-[11px] text-muted-foreground'>
        <div className='flex items-center gap-1.5 pb-1 font-semibold text-foreground'>
          <Info className='size-3.5 text-muted-foreground' />
          <span>{t('region.judgementTitle', '落区与判定逻辑')}</span>
        </div>
        <p className='leading-relaxed text-muted-foreground/90'>
          {t(
            'region.judgementDesc',
            '算法以目标外接框底部中心点做区域归属裁决：命中任一屏蔽区即丢弃；配置了识别区时，目标必须落在识别区内才会被上报。'
          )}
        </p>
      </div>
    </div>
  )
}
