import { type KeyboardEvent, memo, useRef } from 'react'
import {
  Activity,
  Gauge,
  Loader2,
  RotateCcw,
  Search,
  Sparkles,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Slider } from '@/components/ui/slider'

interface ImageSearchControlsProps {
  threshold: number
  onThresholdChange: (val: number) => void
  topK: number
  onTopKChange: (val: number) => void
  onSearch: () => void
  onReset: () => void
  canSearch: boolean
  isSearching: boolean
  uploadProgress: number | null
}

const TOP_K_OPTIONS = [10, 20, 50] as const
const PRESET_THRESHOLDS = [50, 70, 85] as const

export const ImageSearchControls = memo(function ImageSearchControls({
  threshold,
  onThresholdChange,
  topK,
  onTopKChange,
  onSearch,
  onReset,
  canSearch,
  isSearching,
  uploadProgress,
}: ImageSearchControlsProps) {
  const { t } = useTranslation('imageSearch')
  const topKButtonRefs = useRef<Array<HTMLButtonElement | null>>([])

  const thresholdPercent = Math.round(threshold * 100)

  const getThresholdLevel = (percent: number) => {
    if (percent >= 80) {
      return {
        label: t('controls.levelStrict'),
        color: STATUS_LEVEL_CLASS.active,
      }
    }
    if (percent >= 65) {
      return {
        label: t('controls.levelBalanced'),
        color:
          'border-primary/30 bg-primary/10 text-primary dark:border-primary/40 dark:bg-primary/20',
      }
    }
    return {
      label: t('controls.levelRelaxed'),
      color: STATUS_LEVEL_CLASS.warning,
    }
  }

  const levelInfo = getThresholdLevel(thresholdPercent)

  const handleTopKKeyDown = (
    e: KeyboardEvent<HTMLButtonElement>,
    index: number
  ) => {
    if (isSearching) return
    let nextIndex: number | null = null
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      nextIndex = (index + 1) % TOP_K_OPTIONS.length
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      nextIndex = (index - 1 + TOP_K_OPTIONS.length) % TOP_K_OPTIONS.length
    }

    if (nextIndex !== null) {
      e.preventDefault()
      onTopKChange(TOP_K_OPTIONS[nextIndex])
      topKButtonRefs.current[nextIndex]?.focus()
    }
  }

  return (
    <Card className='relative flex flex-col gap-4 overflow-hidden border-border/70 bg-card/60 p-4 shadow-xs backdrop-blur-xs'>
      {/* 相似度阈值滑块设置 */}
      <div className='flex flex-col gap-2'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-1.5'>
            <Gauge className='h-3.5 w-3.5 text-primary' />
            <Label className='text-xs font-semibold text-foreground'>
              {t('controls.threshold')}
            </Label>
          </div>
          <div className='flex items-center gap-1.5'>
            <Badge
              variant='outline'
              className={cn(
                'px-1.5 py-0 text-[10px] font-medium transition-colors',
                levelInfo.color
              )}
            >
              {levelInfo.label}
            </Badge>
            <span className='min-w-10 rounded-md border border-border/60 bg-muted/60 px-2 py-0.5 text-center font-mono text-xs font-bold text-foreground'>
              {t('controls.thresholdValue', { value: thresholdPercent })}
            </span>
          </div>
        </div>

        <Slider
          value={[thresholdPercent]}
          min={0}
          max={100}
          step={5}
          disabled={isSearching}
          aria-label={t('controls.threshold')}
          onValueChange={(val) => {
            if (val[0] !== undefined) {
              onThresholdChange(val[0] / 100)
            }
          }}
          className='py-1'
        />

        {/* 快捷刻度预设与说明 */}
        <div className='flex items-center justify-between text-[11px] text-muted-foreground'>
          <span className='truncate'>{t('controls.thresholdHint')}</span>
          <div className='flex shrink-0 items-center gap-1 ps-1'>
            {PRESET_THRESHOLDS.map((preset) => {
              const labelKey =
                preset === 50
                  ? 'controls.preset50'
                  : preset === 70
                    ? 'controls.preset70'
                    : 'controls.preset85'
              const isSelected = thresholdPercent === preset
              return (
                <Button
                  key={preset}
                  type='button'
                  variant={isSelected ? 'default' : 'secondary'}
                  size='sm'
                  disabled={isSearching}
                  aria-pressed={isSelected}
                  title={t(labelKey, `${preset}%`)}
                  onClick={() => onThresholdChange(preset / 100)}
                  className={cn(
                    'h-5 rounded-md px-1.5 py-0 font-mono text-[10px]',
                    isSelected
                      ? 'font-bold shadow-2xs'
                      : 'text-muted-foreground'
                  )}
                >
                  {preset}%
                </Button>
              )
            })}
          </div>
        </div>
      </div>

      {/* Top-K 候选数量选择（支持方向键漫游 Roving Tabindex） */}
      <div className='flex items-center justify-between gap-4 border-t border-border/50 pt-3'>
        <div className='flex items-center gap-1.5'>
          <Activity className='h-3.5 w-3.5 text-primary' />
          <Label className='text-xs font-semibold text-foreground'>
            {t('controls.topK')}
          </Label>
        </div>

        <div
          role='radiogroup'
          aria-label={t('controls.topK')}
          className='flex items-center rounded-lg border border-border/60 bg-muted/40 p-0.5'
        >
          {TOP_K_OPTIONS.map((opt, index) => {
            const isSelected = topK === opt
            return (
              <Button
                key={opt}
                ref={(el) => {
                  topKButtonRefs.current[index] = el
                }}
                type='button'
                role='radio'
                variant={isSelected ? 'secondary' : 'ghost'}
                size='sm'
                aria-checked={isSelected}
                tabIndex={isSelected ? 0 : -1}
                disabled={isSearching}
                title={t('controls.topKOption', { count: opt })}
                onClick={() => onTopKChange(opt)}
                onKeyDown={(e) => handleTopKKeyDown(e, index)}
                className={cn(
                  'h-6 rounded-md px-2.5 py-0 font-mono text-xs',
                  isSelected
                    ? 'bg-background font-semibold text-foreground shadow-xs hover:bg-background'
                    : 'text-muted-foreground hover:text-foreground'
                )}
              >
                {opt}
              </Button>
            )
          })}
        </div>
      </div>

      {/* 操作按钮区 */}
      <div className='flex items-center gap-2 border-t border-border/50 pt-3'>
        <Button
          type='button'
          className={cn(
            'relative h-9.5 flex-1 gap-2 overflow-hidden text-xs font-semibold shadow-xs transition-all duration-200 active:scale-[0.99]',
            canSearch &&
              !isSearching &&
              'ring-2 shadow-primary/20 ring-primary/25 hover:shadow-md'
          )}
          disabled={!canSearch || isSearching}
          onClick={onSearch}
        >
          {isSearching ? (
            <>
              <Loader2 className='h-4 w-4 animate-spin' />
              <span>
                {uploadProgress !== null && uploadProgress < 100
                  ? t('controls.uploading', { progress: uploadProgress })
                  : t('controls.searching')}
              </span>
            </>
          ) : (
            <>
              <Search className='h-4 w-4' />
              <span>{t('controls.search')}</span>
              {canSearch && <Sparkles className='h-3 w-3 opacity-70' />}
            </>
          )}
        </Button>

        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-9.5 gap-1.5 px-3 text-xs transition-colors hover:border-border hover:bg-muted/60'
          disabled={isSearching}
          onClick={onReset}
        >
          <RotateCcw className='h-3.5 w-3.5' />
          <span>{t('controls.reset')}</span>
        </Button>
      </div>
    </Card>
  )
})
