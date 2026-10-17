import { useState } from 'react'
import { type Column } from '@tanstack/react-table'
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
import { DataTableColumnHeader } from '@/components/data-table'
import { type Camera, type CameraHealth } from '../data/schema'
import { useToggleCameraEnabled } from '../hooks/use-cameras'
import { copyToClipboard } from '../utils/url-helper'

export function CameraColumnHeader({
  column,
  titleKey,
}: {
  column: Column<Camera, unknown>
  titleKey: string
}) {
  const { t } = useTranslation('cameras')
  return <DataTableColumnHeader column={column} title={t(titleKey)} />
}

export function CameraActionsHeader() {
  const { t } = useTranslation('cameras')
  return <span className='sr-only'>{t('table.columns.actions')}</span>
}

const healthStatusConfig: Record<
  CameraHealth,
  { dotClass: string; badgeClass: string }
> = {
  online: {
    dotClass: 'bg-emerald-500',
    badgeClass:
      'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  },
  offline: {
    dotClass: 'bg-zinc-400 dark:bg-zinc-500',
    badgeClass:
      'border-zinc-500/30 bg-zinc-500/10 text-zinc-600 dark:text-zinc-400',
  },
  error: {
    dotClass: 'bg-destructive animate-pulse',
    badgeClass: 'border-destructive/30 bg-destructive/10 text-destructive',
  },
  unknown: {
    dotClass: 'bg-sky-400',
    badgeClass:
      'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300',
  },
}

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
  const config = healthStatusConfig[camera.health] ?? healthStatusConfig.unknown

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
        className={cn(
          'inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-normal capitalize',
          config.badgeClass
        )}
      >
        <span className={cn('size-1.5 rounded-full', config.dotClass)} />
        <span>
          {t(`status.${camera.health}`, { defaultValue: camera.health })}
        </span>
      </Badge>
      {camera.degraded && (
        <span
          className='size-1.5 rounded-full bg-amber-500'
          title={t('status.degraded')}
        />
      )}
      {camera.stale && (
        <span
          className='size-1.5 rounded-full bg-muted-foreground/60'
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
        className='size-6 shrink-0 text-muted-foreground opacity-60 transition-opacity group-hover:opacity-100 hover:text-foreground'
        onClick={handleCopy}
      >
        {copied ? (
          <Check className='size-3.5 text-emerald-500' />
        ) : (
          <Copy className='size-3.5' />
        )}
        <span className='sr-only'>{t('actions.copyRtsp')}</span>
      </Button>
    </div>
  )
}
