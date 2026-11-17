import { useMemo, useState, type SyntheticEvent } from 'react'
import {
  AlertTriangle,
  Download,
  Film,
  Loader2,
  RefreshCw,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type ClipInfo } from '../../data/schema'
import {
  useRecordClipRefresh,
  type ClipRecordKind,
} from '../../hooks/use-record-clip'
import {
  type ClipCodecProbe,
  probeClipCodecSupport,
} from '../../lib/clip-codec'
import { getMediaUrl } from '../common/media-utils'

export interface RecordClipPanelProps {
  kind: ClipRecordKind
  recordId: string
  /** 列表行快照里的片段；刷新后以详情接口返回的为准。 */
  clip: ClipInfo
  /** 编码支持探测，默认走浏览器能力；测试里注入以模拟不支持 HEVC 的环境。 */
  probe?: ClipCodecProbe
}

/** 片段文件名：取 `mediaPath` 的最后一段，下载时作为默认文件名。 */
function clipFileName(mediaPath: string): string {
  const segments = mediaPath.split('/').filter(Boolean)
  return segments[segments.length - 1] || 'clip.mp4'
}

function formatSeconds(ms: number): string {
  return (Math.max(0, ms) / 1000).toFixed(1)
}

/**
 * 告警 / 识别详情里的事件录像面板。
 *
 * - `recording`：文件仍在写，MP4 尾部 moov 未落盘前浏览器无法播放，只给「录制中」与刷新；
 * - `failed`：只给提示，没有可下载的文件；
 * - `completed` / `truncated`：编码可解时内嵌播放并跳到本事件的偏移，否则提示下载。
 *   任何可播状态都提供下载。
 */
export function RecordClipPanel({
  kind,
  recordId,
  clip: snapshotClip,
  probe = probeClipCodecSupport,
}: RecordClipPanelProps) {
  const { t } = useTranslation('records')
  const refreshed = useRecordClipRefresh(kind, recordId)
  // 刷新后后端可能返回 null（片段已被清理）：此时不再展示旧快照；
  // 若列表快照已推进到终态而手动刷新快照仍停留在 recording，以更新的列表快照为准。
  const clip =
    refreshed.clip === undefined
      ? snapshotClip
      : refreshed.clip?.status === 'recording' &&
          snapshotClip.status !== 'recording'
        ? snapshotClip
        : refreshed.clip

  return (
    <div
      className='space-y-3 rounded-lg border border-border bg-card p-4 shadow-xs'
      data-testid='record-clip-panel'
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h4 className='flex items-center gap-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
          <Film className='size-3.5' />
          {t('clip.title')}
        </h4>
        {clip && <ClipStatusBadges clip={clip} />}
      </div>

      {!clip ? (
        <p className='text-xs text-muted-foreground'>{t('clip.unavailable')}</p>
      ) : clip.status === 'recording' ? (
        <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border border-dashed border-border bg-muted/30 px-3 py-2.5'>
          <span className='flex items-center gap-1.5 text-xs text-muted-foreground'>
            <Loader2 className='size-3.5 animate-spin' />
            {t('clip.recordingHint')}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7 gap-1.5 text-xs'
            onClick={refreshed.refresh}
            disabled={refreshed.isRefreshing}
          >
            <RefreshCw
              className={cn(
                'size-3.5',
                refreshed.isRefreshing && 'animate-spin'
              )}
            />
            {t('clip.refresh')}
          </Button>
        </div>
      ) : clip.status === 'failed' || !clip.mediaPath ? (
        <p
          className={cn(
            'flex items-center gap-1.5 rounded-md border px-3 py-2.5 text-xs',
            statusLevelClass('destructive')
          )}
        >
          <AlertTriangle className='size-3.5 shrink-0' />
          {t('clip.failed')}
        </p>
      ) : (
        // 片段内容或事件偏移变化（刷新后换了文件/状态，或同片段切换关联事件）时重建播放器。
        <ClipPlayer
          key={`${clip.id}:${clip.status}:${clip.mediaPath}:${clip.offsetMs}`}
          clip={clip}
          probe={probe}
        />
      )}

      {refreshed.isError && (
        <p className='text-[11px] text-destructive'>
          {t('clip.refreshFailed')}
        </p>
      )}
    </div>
  )
}

function ClipStatusBadges({ clip }: { clip: ClipInfo }) {
  const { t } = useTranslation('records')
  const incomplete =
    clip.status === 'truncated' || (clip.partial && clip.status !== 'failed')

  return (
    <div className='flex flex-wrap items-center gap-1.5'>
      {clip.status === 'recording' && (
        <Badge
          variant='outline'
          className={cn('gap-1.5 text-[11px]', statusLevelClass('warning'))}
        >
          <span
            className={cn(
              'size-1.5 animate-pulse rounded-full',
              statusDotClass('warning')
            )}
          />
          {t('clip.status.recording')}
        </Badge>
      )}
      {clip.status === 'failed' && (
        <Badge
          variant='outline'
          className={cn('gap-1.5 text-[11px]', statusLevelClass('destructive'))}
        >
          <span
            className={cn(
              'size-1.5 rounded-full',
              statusDotClass('destructive')
            )}
          />
          {t('clip.status.failed')}
        </Badge>
      )}
      {incomplete && (
        <Badge
          variant='outline'
          className={cn('gap-1.5 text-[11px]', statusLevelClass('warning'))}
          title={t('clip.partialHint')}
        >
          <span
            className={cn('size-1.5 rounded-full', statusDotClass('warning'))}
          />
          {t('clip.status.partial')}
        </Badge>
      )}
      {clip.status !== 'recording' &&
        clip.status !== 'failed' &&
        clip.durationMs > 0 && (
          <Badge variant='outline' className='font-mono text-[11px]'>
            {t('clip.duration', { seconds: formatSeconds(clip.durationMs) })}
          </Badge>
        )}
    </div>
  )
}

function ClipPlayer({
  clip,
  probe,
}: {
  clip: ClipInfo
  probe: ClipCodecProbe
}) {
  const { t } = useTranslation('records')
  const supported = useMemo(() => probe(clip.codec), [probe, clip.codec])
  // 探测「声称支持」但真实解码失败（如缺硬件 HEVC 解码器）时，退回下载提示。
  const [playbackFailed, setPlaybackFailed] = useState(false)
  const mediaUrl = getMediaUrl(clip.mediaPath)
  const canPlay = supported && !playbackFailed

  const handleLoadedMetadata = (event: SyntheticEvent<HTMLVideoElement>) => {
    const video = event.currentTarget
    const offsetSeconds = clip.offsetMs / 1000
    if (offsetSeconds <= 0) return
    // 偏移来自事件时刻，片段被截断时可能越过实际时长；钳到末尾而不是让 seek 失败。
    video.currentTime = Number.isFinite(video.duration)
      ? Math.min(offsetSeconds, video.duration)
      : offsetSeconds
  }

  return (
    <div className='space-y-2'>
      {canPlay ? (
        <video
          controls
          preload='metadata'
          playsInline
          src={mediaUrl}
          aria-label={t('clip.playerLabel')}
          className='aspect-video w-full rounded-md border border-border bg-black'
          onLoadedMetadata={handleLoadedMetadata}
          onError={() => setPlaybackFailed(true)}
        />
      ) : !supported ? (
        <p
          className={cn(
            'flex items-center gap-1.5 rounded-md border px-3 py-2.5 text-xs',
            statusLevelClass('warning')
          )}
        >
          <Video className='size-3.5 shrink-0' />
          {t('clip.unsupportedCodec')}
        </p>
      ) : (
        <p
          className={cn(
            'flex items-center gap-1.5 rounded-md border px-3 py-2.5 text-xs',
            statusLevelClass('destructive')
          )}
        >
          <AlertTriangle className='size-3.5 shrink-0' />
          {t('clip.playbackFailed')}
        </p>
      )}

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <span className='font-mono text-[11px] text-muted-foreground'>
          {t('clip.offset', { seconds: formatSeconds(clip.offsetMs) })}
          {clip.codec ? ` · ${clip.codec.toUpperCase()}` : ''}
        </span>
        <Button
          asChild
          variant='outline'
          size='sm'
          className='h-7 gap-1.5 text-xs'
        >
          <a href={mediaUrl} download={clipFileName(clip.mediaPath)}>
            <Download className='size-3.5' />
            {t('clip.download')}
          </a>
        </Button>
      </div>
    </div>
  )
}
