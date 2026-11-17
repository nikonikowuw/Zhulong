import { useMemo } from 'react'
import { Film } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { NumberInput } from '@/components/ui/number-input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  CLIP_POST_SECONDS_MAX,
  CLIP_POST_SECONDS_MIN,
  CLIP_PRE_SECONDS_MAX,
  CLIP_PRE_SECONDS_MIN,
  CLIP_RECOGNITION_FILTERS,
  parseClipConfig,
  serializeClipConfig,
  type ClipConfig,
  type ClipRecognitionFilter,
} from '../data/clip-config'

export interface ChannelTaskClipSectionProps {
  algoId: string
  clipConfigJson: string
  /** 只有识别类算法（person_structure）才展示「识别过滤」。 */
  showRecognitionFilter: boolean
  onChange: (clipConfigJson: string) => void
}

function isRecognitionFilter(value: string): value is ClipRecognitionFilter {
  return (CLIP_RECOGNITION_FILTERS as readonly string[]).includes(value)
}

/** 算法检视器里的「事件录像」配置卡：开关、前段 / 后段秒数、识别过滤。 */
export function ChannelTaskClipSection({
  algoId,
  clipConfigJson,
  showRecognitionFilter,
  onChange,
}: ChannelTaskClipSectionProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const config = useMemo(
    () => parseClipConfig(clipConfigJson),
    [clipConfigJson]
  )

  const commit = (patch: Partial<ClipConfig>) => {
    onChange(serializeClipConfig({ ...config, ...patch }))
  }

  const switchId = `clip-enabled-${algoId}`
  const preId = `clip-pre-${algoId}`
  const postId = `clip-post-${algoId}`
  const filterId = `clip-filter-${algoId}`

  return (
    <div className='space-y-3 rounded-xl border border-border bg-card p-4 shadow-xs'>
      <div className='flex items-center justify-between gap-3'>
        <div className='flex flex-col gap-0.5'>
          <div className='flex items-center gap-1.5'>
            <Film className='size-3.5 text-muted-foreground' />
            <Label
              htmlFor={switchId}
              className='text-xs font-semibold text-foreground'
            >
              {t('clip.title')}
            </Label>
          </div>
          <span className='text-[11px] text-muted-foreground'>
            {t('clip.description')}
          </span>
        </div>
        <Switch
          id={switchId}
          checked={config.enabled}
          onCheckedChange={(enabled) => commit({ enabled })}
        />
      </div>

      {config.enabled && (
        <div className='grid grid-cols-2 gap-3'>
          <div className='space-y-1'>
            <Label htmlFor={preId} className='text-[11px] font-medium'>
              {t('clip.preSeconds')}
            </Label>
            <NumberInput
              id={preId}
              integer
              min={CLIP_PRE_SECONDS_MIN}
              max={CLIP_PRE_SECONDS_MAX}
              value={config.preSeconds}
              onValueCommit={(next) => {
                if (next !== undefined) commit({ preSeconds: next })
              }}
              className='h-8 w-full font-mono text-xs'
            />
            <span className='font-mono text-[10px] text-muted-foreground'>
              {t('clip.secondsRange', {
                min: CLIP_PRE_SECONDS_MIN,
                max: CLIP_PRE_SECONDS_MAX,
              })}
            </span>
          </div>
          <div className='space-y-1'>
            <Label htmlFor={postId} className='text-[11px] font-medium'>
              {t('clip.postSeconds')}
            </Label>
            <NumberInput
              id={postId}
              integer
              min={CLIP_POST_SECONDS_MIN}
              max={CLIP_POST_SECONDS_MAX}
              value={config.postSeconds}
              onValueCommit={(next) => {
                if (next !== undefined) commit({ postSeconds: next })
              }}
              className='h-8 w-full font-mono text-xs'
            />
            <span className='font-mono text-[10px] text-muted-foreground'>
              {t('clip.secondsRange', {
                min: CLIP_POST_SECONDS_MIN,
                max: CLIP_POST_SECONDS_MAX,
              })}
            </span>
          </div>

          {showRecognitionFilter && (
            <div className='col-span-2 space-y-1'>
              <Label htmlFor={filterId} className='text-[11px] font-medium'>
                {t('clip.recognitionFilter')}
              </Label>
              <Select
                value={config.recognitionFilter}
                onValueChange={(value) => {
                  if (isRecognitionFilter(value)) {
                    commit({ recognitionFilter: value })
                  }
                }}
              >
                <SelectTrigger id={filterId} className='h-8 w-full text-xs'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CLIP_RECOGNITION_FILTERS.map((filter) => (
                    <SelectItem key={filter} value={filter} className='text-xs'>
                      {t(`clip.filters.${filter}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
