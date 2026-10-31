import { Loader2, RotateCcw, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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

export function ImageSearchControls({
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

  return (
    <Card className='flex flex-col gap-5 border-border/80 bg-card/60 p-4'>
      {/* 相似度阈值滑块 */}
      <div className='flex flex-col gap-2'>
        <div className='flex items-center justify-between text-xs'>
          <Label className='text-xs font-semibold text-foreground'>
            {t('controls.threshold')}
          </Label>
          <span className='rounded bg-muted px-2 py-0.5 font-mono text-xs font-semibold text-primary'>
            {t('controls.thresholdValue', { value: thresholdPercent })}
          </span>
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
        <p className='text-[11px] text-muted-foreground'>
          {t('controls.thresholdHint')}
        </p>
      </div>

      {/* Top-K 候选数量选择 */}
      <div className='flex items-center justify-between gap-4'>
        <Label className='text-xs font-semibold text-foreground'>
          {t('controls.topK')}
        </Label>
        <Select
          value={topK.toString()}
          disabled={isSearching}
          onValueChange={(val) => onTopKChange(parseInt(val, 10))}
        >
          <SelectTrigger className='h-8 w-28 text-xs'>
            <SelectValue placeholder='10' />
          </SelectTrigger>
          <SelectContent>
            {TOP_K_OPTIONS.map((opt) => (
              <SelectItem key={opt} value={opt.toString()} className='text-xs'>
                {t('controls.topKOption', { count: opt })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {/* 操作按钮区 */}
      <div className='flex items-center gap-2 pt-1'>
        <Button
          type='button'
          className='h-9 flex-1 gap-1.5 text-xs font-medium'
          disabled={!canSearch || isSearching}
          onClick={() => onSearch()}
        >
          {isSearching ? (
            <>
              <Loader2 className='h-3.5 w-3.5 animate-spin' />
              {uploadProgress !== null && uploadProgress < 100
                ? t('controls.uploading', { progress: uploadProgress })
                : t('controls.searching')}
            </>
          ) : (
            <>
              <Search className='h-3.5 w-3.5' />
              {t('controls.search')}
            </>
          )}
        </Button>

        <Button
          type='button'
          variant='outline'
          size='sm'
          className='h-9 gap-1 text-xs'
          disabled={isSearching}
          onClick={() => onReset()}
        >
          <RotateCcw className='h-3.5 w-3.5' />
          {t('controls.reset')}
        </Button>
      </div>
    </Card>
  )
}
