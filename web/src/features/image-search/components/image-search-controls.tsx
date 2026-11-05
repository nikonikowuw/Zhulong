import { memo } from 'react'
import {
  Activity,
  Gauge,
  Loader2,
  RotateCcw,
  Search,
  Sparkles,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
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

const TOP_K_OPTIONS = [10, 20, 50]
const PRESET_THRESHOLDS = [50, 70, 85]

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

  const thresholdPercent = Math.round(threshold * 100)

  // 动态等级分类与颜色指示
  const getThresholdLevel = (percent: number) => {
    if (percent >= 80) {
      return {
        label: t('controls.levelStrict'),
        color:
          'text-emerald-600 dark:text-emerald-400 border-emerald-500/30 bg-emerald-500/10',
      }
    }
    if (percent >= 65) {
      return {
        label: t('controls.levelBalanced'),
        color: 'text-primary border-primary/30 bg-primary/10',
      }
    }
    return {
      label: t('controls.levelRelaxed'),
      color:
        'text-amber-600 dark:text-amber-400 border-amber-500/30 bg-amber-500/10',
    }
  }

  const levelInfo = getThresholdLevel(thresholdPercent)

  return (
    <Card className='relative flex flex-col gap-5 overflow-hidden border-border/70 bg-card/60 p-4.5 shadow-xs backdrop-blur-xs'>
      {/* 相似度阈值滑块设置 */}
      <div className='flex flex-col gap-2.5'>
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
              className={`px-1.5 py-0 text-[10px] font-medium transition-colors ${levelInfo.color}`}
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
            {PRESET_THRESHOLDS.map((preset) => (
              <button
                key={preset}
                type='button'
                disabled={isSearching}
                aria-pressed={thresholdPercent === preset}
                onClick={() => onThresholdChange(preset / 100)}
                className={`rounded px-1.5 py-0.5 font-mono text-[10px] transition-colors ${
                  thresholdPercent === preset
                    ? 'bg-primary font-semibold text-primary-foreground'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'
                }`}
              >
                {preset}%
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Top-K 候选数量选择 */}
      <div className='flex items-center justify-between gap-4 border-t border-border/50 pt-3'>
        <div className='flex items-center gap-1.5'>
          <Activity className='h-3.5 w-3.5 text-primary' />
          <Label className='text-xs font-semibold text-foreground'>
            {t('controls.topK')}
          </Label>
        </div>

        {/* 快捷 Segmented Pills */}
        <div
          role='radiogroup'
          aria-label={t('controls.topK')}
          className='flex items-center rounded-lg border border-border/60 bg-muted/40 p-0.5'
        >
          {TOP_K_OPTIONS.map((opt) => (
            <button
              key={opt}
              type='button'
              role='radio'
              aria-checked={topK === opt}
              disabled={isSearching}
              onClick={() => onTopKChange(opt)}
              className={`rounded-md px-2.5 py-0.5 font-mono text-xs transition-all ${
                topK === opt
                  ? 'bg-background font-semibold text-foreground shadow-xs'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {opt}
            </button>
          ))}
        </div>
      </div>

      {/* 操作按钮区 */}
      <div className='flex items-center gap-2 border-t border-border/50 pt-3'>
        <Button
          type='button'
          className='relative h-9.5 flex-1 gap-2 overflow-hidden text-xs font-semibold shadow-xs transition-all duration-200 active:scale-[0.99]'
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
          <RotateCcw className='h-3.5 w-3.5 text-muted-foreground' />
          <span>{t('controls.reset')}</span>
        </Button>
      </div>
    </Card>
  )
})
