import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { type Camera, type CameraHealth } from '../data/schema'
import { useToggleCameraEnabled } from '../hooks/use-cameras'
import { copyToClipboard } from '../utils/url-helper'

const cameraHealthStatusTypes = new Map<CameraHealth, string>([
  ['online', 'bg-teal-100/30 text-teal-900 dark:text-teal-200 border-teal-200'],
  ['offline', 'bg-neutral-300/40 border-neutral-300 text-muted-foreground'],
  ['unknown', 'bg-sky-200/40 text-sky-900 dark:text-sky-100 border-sky-300'],
  [
    'error',
    'bg-destructive/10 dark:bg-destructive/50 text-destructive dark:text-primary border-destructive/10',
  ],
])

export function CameraSwitchCell({ camera }: { camera: Camera }) {
  const toggleMutation = useToggleCameraEnabled()

  return (
    <Switch
      checked={camera.enabled}
      disabled={toggleMutation.isPending}
      onCheckedChange={(checked) => {
        toggleMutation.mutate({
          camera,
          enabled: checked,
        })
      }}
      aria-label='Toggle camera enabled state'
    />
  )
}

export function CameraStatusBadge({ camera }: { camera: Camera }) {
  const { t } = useTranslation('cameras')

  const badgeColor =
    cameraHealthStatusTypes.get(camera.health) ||
    'bg-neutral-300/40 border-neutral-300'

  const tooltipDetails: string[] = []

  if (camera.session) {
    tooltipDetails.push(
      `${t('status.sessionTitle')}: ${t(`status.session.${camera.session}`, {
        defaultValue: camera.session,
      })}`
    )
  }

  if (camera.reason) {
    tooltipDetails.push(`${t('status.error')}: ${camera.reason}`)
  }

  if (camera.degraded) {
    tooltipDetails.push(t('status.degraded'))
  }

  if (camera.stale) {
    tooltipDetails.push(t('status.stale'))
  }

  const badgeContent = (
    <div className='flex items-center gap-1.5'>
      <Badge
        variant='outline'
        className={cn('text-xs font-normal capitalize', badgeColor)}
      >
        {t(`status.${camera.health}`, { defaultValue: camera.health })}
      </Badge>
      {camera.degraded && (
        <span
          className='h-1.5 w-1.5 rounded-full bg-amber-500'
          title={t('status.degraded')}
        />
      )}
      {camera.stale && (
        <span
          className='h-1.5 w-1.5 rounded-full bg-muted-foreground/60'
          title={t('status.stale')}
        />
      )}
    </div>
  )

  if (tooltipDetails.length === 0) {
    return badgeContent
  }

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <div className='cursor-help'>{badgeContent}</div>
        </TooltipTrigger>
        <TooltipContent side='top' className='max-w-xs space-y-1 text-xs'>
          {tooltipDetails.map((detail, index) => (
            <p key={index}>{detail}</p>
          ))}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

export function StreamSpecCell({
  stream,
}: {
  stream?: Camera['streams'][number]
}) {
  const { t } = useTranslation('cameras')
  if (!stream) {
    return (
      <span className='text-xs text-muted-foreground'>
        {t('table.noStreams')}
      </span>
    )
  }

  const resolution =
    stream.width && stream.height ? `${stream.width}x${stream.height}` : null
  const fps = stream.fpsString || (stream.fps ? `${stream.fps} fps` : null)
  const codec = stream.codec?.toUpperCase() || null
  const transport = stream.transport?.toUpperCase() || 'TCP'

  return (
    <div className='flex flex-col text-xs'>
      <div className='flex items-center gap-1 font-medium'>
        {codec && <span className='text-foreground'>{codec}</span>}
        {resolution && (
          <span className='text-muted-foreground'>({resolution})</span>
        )}
      </div>
      <div className='flex items-center gap-1.5 text-[11px] text-muted-foreground'>
        {fps && <span>{fps}</span>}
        <span>•</span>
        <span className='font-mono uppercase'>{transport}</span>
      </div>
    </div>
  )
}

export function CameraRtspCell({ camera }: { camera: Camera }) {
  const { t } = useTranslation('cameras')
  const [copied, setCopied] = useState(false)
  const mainStream = camera.streams?.find((s) => s.role === 'main')

  if (!mainStream?.rtspUrl) {
    return (
      <span className='font-mono text-xs text-muted-foreground'>
        {t('table.noStreams')}
      </span>
    )
  }

  const rtspUrl = mainStream.rtspUrl

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    const success = await copyToClipboard(rtspUrl)
    if (success) {
      setCopied(true)
      toast.success(t('table.copyRtspSuccess'))
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div className='group flex items-center gap-1.5 font-mono text-xs text-muted-foreground'>
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <span className='max-w-[280px] truncate select-all hover:text-foreground'>
              {rtspUrl}
            </span>
          </TooltipTrigger>
          <TooltipContent className='font-mono text-xs'>
            {rtspUrl}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>

      <Button
        variant='ghost'
        size='icon'
        className='h-6 w-6 shrink-0 text-muted-foreground opacity-60 transition-opacity group-hover:opacity-100 hover:text-foreground'
        onClick={handleCopy}
      >
        {copied ? (
          <Check className='h-3.5 w-3.5 text-emerald-500' />
        ) : (
          <Copy className='h-3.5 w-3.5' />
        )}
        <span className='sr-only'>{t('actions.copyRtsp')}</span>
      </Button>
    </div>
  )
}
