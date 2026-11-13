import { useTranslation } from 'react-i18next'
import {
  categoryRegionClass,
  categoryRegionPreviewClass,
  categorySolidClass,
  categoryTextClass,
} from '@/lib/category-palette'
import { cn } from '@/lib/utils'
import {
  MIN_CROP_SIDE,
  REGION_CATEGORY_HUE,
  isFullFrame,
  type Rect,
  type RegionCategory,
  type RegionDraft,
  type RegionItem,
  type RegionSelection,
} from '../data/region-config'
import { type RegionTool } from './region-drag-types'
import { TripwireCanvas } from './tripwire-canvas'
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
  /** 任务级裁剪窗口；`null` 表示全幅。crop 与 ROI/MASK 各自独立，互不影响选中项。 */
  crop?: Rect | null
  onCropChange?: (rect: Rect | null) => void
  cropMinSide?: number
  className?: string
}

const RESIZE_HANDLES = ['tl', 'tr', 'bl', 'br'] as const

/**
 * MASK 斜纹填充。
 *
 * 用 `currentColor` 取色，色相由 `categoryTextClass(hue)` 提供——不在组件里硬编码
 * rgba 色值（theme-and-i18n §1.3.6：配色只能来自查表色板）。
 */
const MASK_HATCH_BACKGROUND =
  'repeating-linear-gradient(45deg, transparent 0 8px, currentColor 8px 16px)'

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
  crop = null,
  onCropChange,
  cropMinSide = MIN_CROP_SIDE,
  className = '',
}: RegionCanvasProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  const {
    containerRef,
    creatingPreview,
    creatingCrop,
    handleBackgroundMouseDown,
    startMove,
    startResize,
    startCropMove,
    startCropResize,
  } = useRegionDrag({
    draft,
    tool: tool === 'tripwire' ? 'select' : tool,
    disabled: disabled || tool === 'tripwire',
    onChange,
    onCreate,
    onSelect,
    onBeforeChange,
    crop,
    onCropChange,
    cropMinSide,
  })

  const cropRect = crop && !isFullFrame(crop) ? crop : null
  const cropEditable = tool === 'crop' && !disabled

  const renderItem = (
    item: RegionItem,
    category: RegionCategory,
    idx: number
  ) => {
    const isSelected = selected?.category === category && selected.index === idx
    const hue = REGION_CATEGORY_HUE[category]
    const isMask = category === 'mask'
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
        className={cn(
          'absolute border-2 transition-shadow',
          categoryRegionClass(hue),
          isSelected
            ? 'shadow-lg ring-2 ring-primary ring-offset-1'
            : 'hover:border-foreground/60',
          disabled ? 'cursor-not-allowed' : 'cursor-move'
        )}
        onMouseDown={startMove(category, idx, item.rect)}
      >
        {isMask && (
          <span
            aria-hidden
            className={cn(
              'pointer-events-none absolute inset-0 opacity-20',
              categoryTextClass(hue)
            )}
            style={{ backgroundImage: MASK_HATCH_BACKGROUND }}
          />
        )}

        {/* 顶部标签药丸 */}
        <span
          className={cn(
            'absolute top-0 left-0 -translate-y-full rounded-t-sm px-1.5 py-0.5 font-mono text-[10px] leading-none font-bold text-white shadow-xs',
            categorySolidClass(hue)
          )}
        >
          {category.toUpperCase()} #{idx + 1}
        </span>

        {/* 底部中心判定锚点：落区判定基准点 */}
        <div
          title={t('region.bottomCenterAnchor', '底部中心判定锚点')}
          className='absolute bottom-0 left-1/2 size-2.5 -translate-x-1/2 translate-y-1/2 rounded-full border border-background bg-primary shadow-xs ring-1 ring-black/40'
        />

        {/* 四角缩放手柄 */}
        {isSelected && !disabled && (
          <>
            {RESIZE_HANDLES.map((handle) => (
              <div
                key={handle}
                className={cn(
                  'absolute size-3 rounded-full border-2 border-primary bg-background shadow-md transition-transform hover:scale-125',
                  HANDLE_POSITION_CLASS[handle]
                )}
                onMouseDown={startResize(category, idx, handle, item.rect)}
              />
            ))}
          </>
        )}
      </div>
    )
  }

  const renderCrop = (rect: Rect) => {
    const [x1, y1, x2, y2] = rect
    return (
      <div
        style={{
          left: `${x1 * 100}%`,
          top: `${y1 * 100}%`,
          width: `${(x2 - x1) * 100}%`,
          height: `${(y2 - y1) * 100}%`,
        }}
        className={cn(
          'absolute border-2 transition-shadow',
          categoryRegionClass('cyan'),
          categoryTextClass('cyan'),
          cropEditable
            ? 'cursor-move shadow-lg ring-2 ring-current/50'
            : 'pointer-events-none'
        )}
        onMouseDown={startCropMove(rect)}
      >
        <span
          className={cn(
            'absolute top-0 left-0 -translate-y-full rounded-t-sm px-1.5 py-0.5 font-mono text-[10px] leading-none font-bold text-white shadow-xs',
            categorySolidClass('cyan')
          )}
        >
          CROP
        </span>

        {cropEditable && (
          <>
            {RESIZE_HANDLES.map((handle) => (
              <div
                key={handle}
                className={cn(
                  'absolute size-3 rounded-full border-2 border-current bg-background shadow-md transition-transform hover:scale-125',
                  categoryTextClass('cyan'),
                  HANDLE_POSITION_CLASS[handle]
                )}
                onMouseDown={startCropResize(handle, rect)}
              />
            ))}
          </>
        )}
      </div>
    )
  }

  /** 窗口外的压暗层：四块矩形拼出“洞”，比 clip-path 更易读且不影响命中测试。 */
  const renderCropDim = (rect: Rect) => {
    const [x1, y1, x2, y2] = rect
    const dimClass = 'pointer-events-none absolute bg-black/50'
    return (
      <>
        <div
          className={dimClass}
          style={{ left: 0, right: 0, top: 0, height: `${y1 * 100}%` }}
        />
        <div
          className={dimClass}
          style={{ left: 0, right: 0, top: `${y2 * 100}%`, bottom: 0 }}
        />
        <div
          className={dimClass}
          style={{
            left: 0,
            width: `${x1 * 100}%`,
            top: `${y1 * 100}%`,
            height: `${(y2 - y1) * 100}%`,
          }}
        />
        <div
          className={dimClass}
          style={{
            left: `${x2 * 100}%`,
            right: 0,
            top: `${y1 * 100}%`,
            height: `${(y2 - y1) * 100}%`,
          }}
        />
      </>
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
          : tool === 'roi' || tool === 'mask' || tool === 'crop'
            ? 'cursor-crosshair'
            : 'cursor-default'
      } ${className}`}
    >
      {/* 压暗层必须在图元之前渲染，否则会把 ROI/MASK 一起盖住。 */}
      {cropRect && renderCropDim(cropRect)}

      {draft.roi.map((item, i) => renderItem(item, 'roi', i))}
      {draft.mask.map((item, i) => renderItem(item, 'mask', i))}

      {cropRect && renderCrop(cropRect)}
      <TripwireCanvas
        draft={draft}
        drawing={tool === 'tripwire'}
        editable={!disabled && (tool === 'select' || tool === 'tripwire')}
        onChange={onChange}
        onBeforeChange={onBeforeChange}
      />

      {creatingPreview && (
        <div
          style={{
            left: `${creatingPreview[0] * 100}%`,
            top: `${creatingPreview[1] * 100}%`,
            width: `${(creatingPreview[2] - creatingPreview[0]) * 100}%`,
            height: `${(creatingPreview[3] - creatingPreview[1]) * 100}%`,
          }}
          className={`pointer-events-none absolute border-2 border-dashed shadow-xs ${
            creatingCrop
              ? categoryRegionPreviewClass('cyan')
              : categoryRegionPreviewClass(previewHue)
          }`}
        />
      )}
    </div>
  )
}
