import { AlertTriangle, FileArchive, FileSpreadsheet } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { type RecordsTab } from '../../data/schema'
import {
  DEFAULT_MAX_RECORDS,
  HARD_MAX_IMAGES,
} from '../../lib/export-zip-pipeline'

export interface RecordsExportOptionsPanelProps {
  mode: 'selection' | 'batch'
  estimatedTotal: number
  selectedTabs: Set<RecordsTab>
  onToggleTab: (tab: RecordsTab) => void
  exportCsv: boolean
  onExportCsvChange: (val: boolean) => void
  exportImages: boolean
  onExportImagesChange: (val: boolean) => void
  burnOverlay: boolean
  onBurnOverlayChange: (val: boolean) => void
  imageKind: 'scene' | 'closeup' | 'both'
  onImageKindChange: (val: 'scene' | 'closeup' | 'both') => void
  limitImages: boolean
  onLimitImagesChange: (val: boolean) => void
  maxImagesText: string
  onMaxImagesTextChange: (val: string) => void
  onMaxImagesBlur: () => void
}

export function RecordsExportOptionsPanel({
  mode,
  estimatedTotal,
  selectedTabs,
  onToggleTab,
  exportCsv,
  onExportCsvChange,
  exportImages,
  onExportImagesChange,
  burnOverlay,
  onBurnOverlayChange,
  imageKind,
  onImageKindChange,
  limitImages,
  onLimitImagesChange,
  maxImagesText,
  onMaxImagesTextChange,
  onMaxImagesBlur,
}: RecordsExportOptionsPanelProps) {
  const { t } = useTranslation('records')
  const overRecordBudget =
    mode === 'batch' && estimatedTotal > DEFAULT_MAX_RECORDS

  return (
    <div className='space-y-5 py-2 text-sm'>
      {/* 批量模式下的 Tab 自由多维组合 */}
      {mode === 'batch' && (
        <div className='space-y-2'>
          <Label className='text-xs font-semibold text-foreground'>
            {t('export.dimensionScope')}
          </Label>
          <div className='grid grid-cols-2 gap-2.5 rounded-lg border border-border p-3'>
            {(
              [
                { key: 'recognitions', label: t('tabs.recognitions') },
                { key: 'alarms', label: t('tabs.alarms') },
                { key: 'captures', label: t('tabs.captures') },
                { key: 'vehicles', label: t('tabs.vehicles') },
              ] as const
            ).map(({ key, label }) => (
              <label
                key={key}
                className='flex cursor-pointer items-center gap-2 text-xs font-medium'
              >
                <Checkbox
                  checked={selectedTabs.has(key)}
                  onCheckedChange={() => onToggleTab(key)}
                />
                <span>{label}</span>
              </label>
            ))}
          </div>
          {overRecordBudget && (
            <div
              role='alert'
              className={cn(
                'flex items-start gap-2 rounded border p-2 text-[11px]',
                statusLevelClass('destructive')
              )}
            >
              <AlertTriangle className='mt-0.5 size-3.5 shrink-0' />
              <span>
                {t('export.overRecordBudget', {
                  total: estimatedTotal,
                  limit: DEFAULT_MAX_RECORDS,
                })}
              </span>
            </div>
          )}
        </div>
      )}

      {/* 导出内容类型 */}
      <div className='space-y-3'>
        <Label className='text-xs font-semibold text-foreground'>
          {t('export.contentOptions')}
        </Label>

        <div className='space-y-2.5 rounded-lg border border-border p-3'>
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <FileSpreadsheet className='size-4 text-emerald-500' />
              <div>
                <Label
                  htmlFor='export-csv'
                  className='cursor-pointer text-xs font-medium'
                >
                  {t('export.csvReport')}
                </Label>
                <div className='text-[11px] text-muted-foreground'>
                  {t('export.csvDesc')}
                </div>
              </div>
            </div>
            <Switch
              id='export-csv'
              checked={exportCsv}
              onCheckedChange={onExportCsvChange}
            />
          </div>

          <div className='h-px bg-border/60' />

          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <FileArchive className='size-4 text-sky-500' />
              <div>
                <Label
                  htmlFor='export-images'
                  className='cursor-pointer text-xs font-medium'
                >
                  {t('export.imageEvidence')}
                </Label>
                <div className='text-[11px] text-muted-foreground'>
                  {t('export.imageDesc')}
                </div>
              </div>
            </div>
            <Switch
              id='export-images'
              checked={exportImages}
              onCheckedChange={onExportImagesChange}
            />
          </div>
        </div>
      </div>

      {/* 图片相关配置 */}
      {exportImages && (
        <div className='animate-in space-y-3 rounded-lg border border-border bg-muted/30 p-3 duration-200 fade-in'>
          <div className='flex items-center justify-between'>
            <div>
              <Label
                htmlFor='export-burn-overlay'
                className='cursor-pointer text-xs font-medium text-foreground'
              >
                {t('export.burnOverlay')}
              </Label>
              <div className='text-[11px] text-muted-foreground'>
                {t('export.burnOverlayDesc')}
              </div>
            </div>
            <Switch
              id='export-burn-overlay'
              checked={burnOverlay}
              onCheckedChange={onBurnOverlayChange}
            />
          </div>

          <div className='h-px bg-border/50' />

          <div className='space-y-1.5'>
            <Label className='text-xs font-medium text-muted-foreground'>
              {t('export.imageKind')}
            </Label>
            <RadioGroup
              value={imageKind}
              onValueChange={(val) =>
                onImageKindChange(val as 'scene' | 'closeup' | 'both')
              }
              className='grid grid-cols-3 gap-2'
            >
              <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                <RadioGroupItem value='scene' />
                <span>{t('export.sceneOnly')}</span>
              </label>
              <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                <RadioGroupItem value='closeup' />
                <span>{t('export.closeupOnly')}</span>
              </label>
              <label className='flex cursor-pointer items-center gap-1.5 text-xs'>
                <RadioGroupItem value='both' />
                <span>{t('export.bothImages')}</span>
              </label>
            </RadioGroup>
          </div>

          <div className='h-px bg-border/50' />
          <div className='flex items-center justify-between gap-4'>
            <div className='flex items-center gap-2'>
              <Checkbox
                id='limitImages'
                checked={limitImages}
                onCheckedChange={(c) => onLimitImagesChange(Boolean(c))}
              />
              <Label
                htmlFor='limitImages'
                className='cursor-pointer text-xs font-normal'
              >
                {t('export.limitImages')}
              </Label>
            </div>

            {limitImages && (
              <div className='flex items-center gap-1.5'>
                <Input
                  type='number'
                  min={1}
                  max={HARD_MAX_IMAGES}
                  aria-label={t('export.limitImages')}
                  value={maxImagesText}
                  onChange={(e) => onMaxImagesTextChange(e.target.value)}
                  onBlur={onMaxImagesBlur}
                  className='h-7 w-20 font-mono text-xs'
                />
                <span className='text-[11px] text-muted-foreground'>
                  {t('export.unitSheets')}
                </span>
              </div>
            )}
          </div>

          <p className='text-[11px] text-muted-foreground'>
            {t('export.hardCapHint', { limit: HARD_MAX_IMAGES })}
          </p>

          {estimatedTotal > 200 && (
            <div
              className={cn(
                'flex items-start gap-2 rounded border p-2 text-[11px]',
                statusLevelClass('warning')
              )}
            >
              <AlertTriangle className='mt-0.5 size-3.5 shrink-0' />
              <span>{t('export.largeVolumeWarning')}</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
