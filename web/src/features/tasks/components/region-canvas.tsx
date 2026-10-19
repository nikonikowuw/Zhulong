import { useTranslation } from 'react-i18next'
import {
  categoryRegionClass,
  categoryRegionPreviewClass,
  categorySolidClass,
} from '@/lib/category-palette'
import {
  REGION_CATEGORY_HUE,
  type Rect,
  type RegionCategory,
  type RegionDraft,
  type RegionItem,
  type RegionSelection,
} from '../data/region-config'
import { type RegionTool } from './region-drag-types'
import { useRegionDrag } from './use-region-drag'

export type { RegionTool }

export interface RegionCanvasProps {
  tool: RegionTool
  draft: RegionDraft
  selected: RegionSelection | null
  disabled?: boolean
  onChange: (next: RegionDraft) => void
  onCreate: (category: RegionCategory, rect: Rect) => void
  onSelect: (selection: RegionSelection | null) => void
  onBeforeChange?: () => void
  className?: string
}

const RESIZE_HANDLES = ['tl', 'tr', 'bl', 'br'] as const

const HANDLE_POSITION_CLASS: Record<(typeof RESIZE_HANDLES)[number], string> = {
  tl: 'top-0 left-0 -translate-x-1/2 -translate-y-1/2 cursor-nwse-resize',
  tr: 'top-0 right-0 translate-x-1/2 -translate-y-1/2 cursor-nesw-resize',
  bl: 'bottom-0 left-0 -translate-x-1/2 translate-y-1/2 cursor-nesw-resize',
  br: 'bottom-0 right-0 translate-x-1/2 translate-y-1/2 cursor-nwse-resize',
}

export function RegionCanvas({
  tool,
  draft,
  selected,
  disabled = false,
  onChange,
  onCreate,
  onSelect,
  onBeforeChange,
  className = '',
}: RegionCanvasProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const {
    containerRef,
    creatingPreview,
    handleBackgroundMouseDown,
    startMove,
    startResize,
  } = useRegionDrag({
    draft,
    tool,
    disabled,
    onChange,
    onCreate,
    onSelect,
    onBeforeChange,
  })

  const renderItem = (
    item: RegionItem,
    category: RegionCategory,
    idx: number
  ) => {
    const isSelected = selected?.category === category && selected.index === idx
    const hue = REGION_CATEGORY_HUE[category]
    const [x1, y1, x2, y2] = item.rect

    return (
      <div
        key={item.id}
        style={{
          left: `${x1 * 100}%`,
          top: `${y1 * 100}%`,
          width: `${(x2 - x1) * 100}%`,
          height: `${(y2 - y1) * 100}%`,
        }}
        className={`absolute border-2 ${categoryRegionClass(hue)} ${
          isSelected ? 'ring-2 ring-primary ring-offset-1' : ''
        } ${disabled ? 'cursor-not-allowed' : 'cursor-move'}`}
        onMouseDown={startMove(category, idx, item.rect)}
      >
        <span
          className={`absolute top-0 left-0 -translate-y-full rounded-t px-1 py-0.5 font-mono text-[10px] leading-none font-bold text-white ${categorySolidClass(
            hue
          )}`}
        >
          {category.toUpperCase()} #{idx + 1}
        </span>

        {/* 底部中心判定锚点：过滤判定所用的落点 */}
        <div
          title={t('region.bottomCenterAnchor')}
          className='absolute bottom-0 left-1/2 h-2.5 w-2.5 -translate-x-1/2 translate-y-1/2 rounded-full border border-background bg-primary shadow'
        />

        {isSelected && !disabled && (
          <>
            {RESIZE_HANDLES.map((handle) => (
              <div
                key={handle}
                className={`absolute h-3 w-3 rounded-full border-2 border-primary bg-white shadow ${
                  HANDLE_POSITION_CLASS[handle]
                }`}
                onMouseDown={startResize(category, idx, handle, item.rect)}
              />
            ))}
          </>
        )}
      </div>
    )
  }

  const previewHue =
    tool === 'roi' || tool === 'mask' ? REGION_CATEGORY_HUE[tool] : undefined

  return (
    <div
      ref={containerRef}
      onMouseDown={handleBackgroundMouseDown}
      className={`relative h-full w-full overflow-hidden select-none ${
        disabled
          ? 'cursor-not-allowed'
          : tool === 'roi' || tool === 'mask'
            ? 'cursor-crosshair'
            : 'cursor-default'
      } ${className}`}
    >
      {draft.roi.map((item, i) => renderItem(item, 'roi', i))}
      {draft.mask.map((item, i) => renderItem(item, 'mask', i))}

      {creatingPreview && (
        <div
          style={{
            left: `${creatingPreview[0] * 100}%`,
            top: `${creatingPreview[1] * 100}%`,
            width: `${(creatingPreview[2] - creatingPreview[0]) * 100}%`,
            height: `${(creatingPreview[3] - creatingPreview[1]) * 100}%`,
          }}
          className={`pointer-events-none absolute border-2 border-dashed ${categoryRegionPreviewClass(
            previewHue
          )}`}
        />
      )}
    </div>
  )
}
