import { useEffect, useRef, useState } from 'react'
import { type Column, type Row, type Table } from '@tanstack/react-table'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  STATUS_TEXT_CLASS,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableColumnHeader } from '@/components/data-table'
import { type Camera, type CameraHealth } from '../data/schema'
import { useToggleCameraEnabled } from '../hooks/use-cameras'
import { maskRtspUrl } from '../utils/url-helper'

export function CameraSelectAllCheckbox({ table }: { table: Table<Camera> }) {
  const { t } = useTranslation('cameras')
  return (
    <Checkbox
      checked={
        table.getIsAllPageRowsSelected() ||
        (table.getIsSomePageRowsSelected() && 'indeterminate')
      }
      onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
      aria-label={t('table.selectAll')}
      className='translate-y-0.5'
    />
  )
}

export function CameraSelectRowCheckbox({ row }: { row: Row<Camera> }) {
  const { t } = useTranslation('cameras')
  return (
    <Checkbox
      checked={row.getIsSelected()}
      onCheckedChange={(value) => row.toggleSelected(!!value)}
      aria-label={t('table.selectRow', { name: row.original.name })}
      className='translate-y-0.5'
    />
  )
}

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

/**
 * §1.3.3 经典四级状态语义调色板（必须成对具备双模支持）。
 * 类名统一取自 `@/lib/status-palette`，禁止就地改色。
 */
const LEVEL_ACTIVE = STATUS_LEVEL_CLASS.active
const LEVEL_DESTRUCTIVE = STATUS_LEVEL_CLASS.destructive
const LEVEL_INACTIVE = STATUS_LEVEL_CLASS.inactive

const DOT_ACTIVE = STATUS_DOT_CLASS.active
const DOT_DESTRUCTIVE = STATUS_DOT_CLASS.destructive
const DOT_INACTIVE = STATUS_DOT_CLASS.inactive

/** online → 活跃；offline/error → 破坏性（规范将 Offline 归入该档）；unknown → 未激活 / 未知。 */
const healthStatusConfig: Record<
  CameraHealth,
  { dotClass: string; badgeClass: string }
> = {
  online: { dotClass: DOT_ACTIVE, badgeClass: LEVEL_ACTIVE },
  offline: { dotClass: DOT_DESTRUCTIVE, badgeClass: LEVEL_DESTRUCTIVE },
  error: { dotClass: DOT_DESTRUCTIVE, badgeClass: LEVEL_DESTRUCTIVE },
  unknown: { dotClass: DOT_INACTIVE, badgeClass: LEVEL_INACTIVE },
}

export function CameraSwitchCell({ camera }: { camera: Camera }) {
  const { t } = useTranslation('cameras')
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
      aria-label={t('actions.toggleEnabled', { name: camera.name })}
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
          className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS.warning)}
          title={t('status.degraded')}
        />
      )}
      {camera.stale && (
        <span
          className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS.inactive)}
          title={t('status.stale')}
        />
      )}
    </div>
  )

  if (tooltipDetails.length === 0) {
    return badgeContent
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div className='cursor-help'>{badgeContent}</div>
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs space-y-1 text-xs'>
        {tooltipDetails.map((detail) => (
          <p key={detail}>{detail}</p>
        ))}
      </TooltipContent>
    </Tooltip>
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
  const copyTimerRef = useRef<number | null>(null)
  const mainStream = camera.streams?.find((s) => s.role === 'main')

  useEffect(() => {
    return () => {
      if (copyTimerRef.current !== null) {
        clearTimeout(copyTimerRef.current)
      }
    }
  }, [])

  if (!mainStream?.rtspUrl) {
    return (
      <span className='font-mono text-xs text-muted-foreground'>
        {t('table.noStreams')}
      </span>
    )
  }

  const rtspUrl = mainStream.rtspUrl
  const maskedRtspUrl = maskRtspUrl(rtspUrl)

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    const success = await copyToClipboard(rtspUrl)
    if (success) {
      setCopied(true)
      toast.success(t('table.copyRtspSuccess'))
      if (copyTimerRef.current !== null) {
        clearTimeout(copyTimerRef.current)
      }
      copyTimerRef.current = window.setTimeout(() => {
        copyTimerRef.current = null
        setCopied(false)
      }, 2000)
    }
  }

  return (
    <div className='group flex items-center gap-1.5 font-mono text-xs text-muted-foreground'>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className='max-w-[280px] truncate select-all hover:text-foreground'>
            {maskedRtspUrl}
          </span>
        </TooltipTrigger>
        <TooltipContent className='font-mono text-xs'>
          {maskedRtspUrl}
        </TooltipContent>
      </Tooltip>

      <Button
        variant='ghost'
        size='icon'
        className='size-6 shrink-0 text-muted-foreground opacity-60 transition-opacity group-hover:opacity-100 hover:text-foreground'
        onClick={handleCopy}
      >
        {copied ? (
          <Check className={cn('size-3.5', STATUS_TEXT_CLASS.active)} />
        ) : (
          <Copy className='size-3.5' />
        )}
        <span className='sr-only'>{t('actions.copyRtsp')}</span>
      </Button>
    </div>
  )
}
