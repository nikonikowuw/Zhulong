import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { Camera, Loader2, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useStreamConnection } from '../hooks/use-stream-connection'
import { StreamConnectionPool } from '../services/stream-pool'
import { isWebCodecsSupported } from '../services/webcodecs-decoder'
import {
  type GridLayout,
  type LiveCameraItem,
  type LiveCellState,
  type ParsedPacket,
  type StreamType,
} from '../types'
import { LiveFaceOverlay } from './live-face-overlay'
import { LivePersonOverlay } from './live-person-overlay'
import {
  LivePlayer,
  type LivePlayerAudioStatus,
  type LivePlayerRef,
} from './live-player'
import { LivePlayerCellFooter } from './live-player-cell-footer'
import { LivePlayerCellHeader } from './live-player-cell-header'
import { LivePlayerEmptyCell } from './live-player-empty-cell'
import { LiveVehicleOverlay } from './live-vehicle-overlay'
import {
  MockStreamCanvas,
  type MockStreamCanvasRef,
} from './mock-stream-canvas'

interface LivePlayerCellProps {
  cell: LiveCellState
  layout?: GridLayout
  isActive: boolean
  isMaximized: boolean
  showAIOverlay?: boolean
  onSelect: () => void
  onClear: () => void
  onToggleMaximize: () => void
  onSetMute: (isMuted: boolean) => void
  onSetStreamType: (streamType: StreamType) => void
  onDropCamera?: (camera: LiveCameraItem) => void
  /** 全局音量 0..1，只在解禁后、有音的格子上生效。 */
  volume?: number
}

function getActiveCellBorderClass(
  isDragOver: boolean,
  isActive: boolean
): string {
  if (isDragOver) {
    return 'border-primary shadow-[0_0_24px_rgba(var(--primary),0.35)] ring-2 ring-primary/40'
  }
  if (isActive) {
    return 'border-primary/90 shadow-[0_0_16px_rgba(var(--primary),0.2)] ring-1 ring-primary/40'
  }
  return 'border-border/50 hover:border-border/80'
}

export const LivePlayerCell = memo(function LivePlayerCell({
  cell,
  layout = 4,
  isActive,
  isMaximized,
  showAIOverlay = true,
  onSelect,
  onClear,
  onToggleMaximize,
  onSetMute,
  onSetStreamType,
  onDropCamera,
  volume = 1,
}: LivePlayerCellProps): React.JSX.Element {
  const { t } = useTranslation('live')
  const [useMockCanvas, setUseMockCanvas] = useState(false)
  const [isDragOver, setIsDragOver] = useState(false)
  /**
   * 是否已经出过首个画面。
   *
   * WebSocket 建立（status=connected）远早于首帧：服务端要先把 RTSP 连接建起来
   * （实测 open 1.2s + 探测数百毫秒），这 1~2 秒里如果只按 status 判断，
   * 遮罩会被撤掉、露出纯黑画面 —— 看起来像卡死，实际上是在等视频源。
   */
  const [hasFirstFrame, setHasFirstFrame] = useState(false)
  /**
   * 本播放器能否真的解码。
   *
   * ⚠️ 「连接已建立但首帧没来」的等待遮罩必须叠加这个前提：
   *    局域网 HTTP（非安全上下文）下 WebCodecs 不可用，LivePlayer 会渲染
   *    「需要安全上下文 / 切换至 HTTPS」的引导面板，此时首帧永远不会到达。
   *    只看 status 的话，遮罩会把那个引导按钮永久盖住，用户既看不到原因也点不到出路。
   */
  const [supportsWebCodecs] = useState(() => isWebCodecsSupported())
  const playerRef = useRef<LivePlayerRef | null>(null)
  const mockCanvasRef = useRef<MockStreamCanvasRef | null>(null)

  // 单例单次订阅：数据包由 useStreamConnection 接收并分发给渲染器
  const handlePacket = useCallback((packet: ParsedPacket) => {
    playerRef.current?.decode(packet)
  }, [])

  /**
   * 音频可播性（由 `LivePlayer` 回报 + 能力帧共同决定）。
   *
   * 声明在 `cellSourceKey` 那条复位 effect 之前：换机 / 切码流时它必须一起复位，
   * 否则新格子会顶着上一路流的「无音频」结论，喇叭一直灰着。
   */
  const [audioStatus, setAudioStatus] = useState<LivePlayerAudioStatus>('idle')

  // 切换机位或码流即视为重新起播，等待遮罩重新出现
  const handleFirstFrame = useCallback(() => setHasFirstFrame(true), [])
  const cellSourceKey = `${cell.cameraId ?? ''}:${cell.streamType}`
  useEffect(() => {
    setHasFirstFrame(false)
    // 换机 / 切码流：音频能力属于上一条流，音频降级/无音频的状态也要跟着复位。
    setAudioStatus('idle')
  }, [cellSourceKey])

  const { status, errorMessage, stats, retry, streamInfo } =
    useStreamConnection({
      cameraId: cell.cameraId,
      streamType: cell.streamType,
      enabled: !useMockCanvas && Boolean(cell.cameraId),
      onPacket: handlePacket,
    })

  /**
   * 本格的音频可播性。
   *
   * 直接就近取 `streamInfo`（同一个 hook 就在本组件里调），**不**从 `live-grid` /
   * 日志层一路穿 16 个格子：`streamInfo` 是每条流自己的属性，穿层只会在中间某一层
   * 漏传时静默退化成「全都不出声」。
   *
   * 两种置灰：
   *   - `streamInfo == null`（旧后端不发能力帧 / 还没收到）→ `audioChecking`（**尚不确定**）；
   *   - `audio.present === false` → `noAudio`（已确定性：这路就是没声音）；
   *   - 有音轨但格式浏览器不支持（解码器降级）→ `audioUnsupported`。
   *
   * ⚠️ 「还不知道」和「确定没有」必须是两种文案：
   * 能力帧只在建连时下发一次，从点开到收到有几帧的延迟。若把它归成 `noAudio`，
   * 一个**完全正常**的相机在首帧期间就会弹「该路无音频」，用户看到的是设备故障。
   */
  const capabilityAudioAvailable = streamInfo?.audio.present === true
  const audioDisabledReason: string | null = capabilityAudioAvailable
    ? audioStatus === 'unsupported'
      ? t('audioUnsupported')
      : audioStatus === 'no_audio'
        ? t('noAudio')
        : null
    : streamInfo == null
      ? t('audioChecking')
      : t('noAudio')
  const audioDisabled = audioDisabledReason !== null

  /**
   * 等待遮罩的文案；null 表示不需要遮罩。
   *
   * 用 if 链而不是嵌套三元：三种状态的含义差别很大，嵌套三元得数括号才读得清。
   * 重连时即使没有错误文案也要保留遮罩（转圈本身就是反馈）。
   */
  const waitingText = ((): string | null => {
    if (useMockCanvas || !supportsWebCodecs) return null
    if (status === 'connecting') return t('connecting')
    if (status === 'reconnecting') return errorMessage ?? ''
    if (status === 'connected' && !hasFirstFrame) {
      return t('frame.waitingFirstFrame')
    }
    return null
  })()

  // 播放器识别出实际视频分辨率时，回流更新连接池状态
  const handlePlayerStatsChange = useCallback(
    (newStats: { resolution?: string }) => {
      if (cell.cameraId && newStats.resolution) {
        StreamConnectionPool.getInstance().updateStats(
          cell.cameraId,
          cell.streamType,
          { resolution: newStats.resolution }
        )
      }
    },
    [cell.cameraId, cell.streamType]
  )

  const handleScreenshot = () => {
    if (useMockCanvas) {
      mockCanvasRef.current?.screenshot(cell.cameraName)
    } else {
      playerRef.current?.screenshot(cell.cameraName)
    }
  }

  const handleToggleMute = () => {
    // 无音频的格子：按钮是 disabled，但在 disabled 属性被间隙性绕过（键盘/title 点击）
    // 的情况下也不应该产生焦点 —— 焦点一旦落地，下一格再点解禁就会被互斥逻辑挤掉。
    if (audioDisabled) return
    const nextMute = !cell.isMuted
    onSetMute(nextMute)
    playerRef.current?.setMute(nextMute)
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
    if (!isDragOver) setIsDragOver(true)
  }

  const handleDragLeave = () => {
    setIsDragOver(false)
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragOver(false)
    try {
      const dataStr = e.dataTransfer.getData('application/json')
      if (dataStr) {
        const camera = JSON.parse(dataStr) as LiveCameraItem
        if (camera && camera.id) {
          onDropCamera?.(camera)
        }
      }
    } catch {
      // 忽略非法拖拽数据
    }
  }

  const isMicro = layout === 16
  const isCompact = layout === 9 || isMicro

  if (!cell.cameraId) {
    return (
      <LivePlayerEmptyCell
        cell={cell}
        isActive={isActive}
        isDragOver={isDragOver}
        isMicro={isMicro}
        isCompact={isCompact}
        onSelect={onSelect}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      />
    )
  }

  return (
    <div
      onClick={onSelect}
      onDoubleClick={onToggleMaximize}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      className={`group relative flex h-full w-full overflow-hidden rounded-xl border bg-black text-white transition-all select-none ${getActiveCellBorderClass(
        isDragOver,
        isActive
      )}`}
    >
      {/* 拖拽释放高亮指示浮层 */}
      {isDragOver && (
        <div className='absolute inset-0 z-30 flex flex-col items-center justify-center bg-primary/25 backdrop-blur-xs'>
          <div className='animate-bounce rounded-full bg-primary p-3 text-primary-foreground shadow-lg ring-4 ring-primary/30'>
            <Camera className='h-6 w-6' />
          </div>
          <p className='mt-2 text-xs font-semibold text-white drop-shadow-md'>
            {t('releaseToPlay')}
          </p>
        </div>
      )}

      {/* 视频画面主体 */}
      <div className='relative h-full w-full'>
        {useMockCanvas ? (
          <>
            <MockStreamCanvas
              ref={mockCanvasRef}
              cameraName={cell.cameraName}
              streamType={cell.streamType}
            />
            {showAIOverlay && cell.cameraId && (
              <>
                <LivePersonOverlay
                  cameraId={cell.cameraId}
                  enabled={showAIOverlay}
                />
                <LiveFaceOverlay
                  cameraId={cell.cameraId}
                  enabled={showAIOverlay}
                />
                <LiveVehicleOverlay
                  cameraId={cell.cameraId}
                  enabled={showAIOverlay}
                />
              </>
            )}
          </>
        ) : (
          <LivePlayer
            ref={playerRef}
            cameraId={cell.cameraId}
            streamType={cell.streamType}
            showAIOverlay={showAIOverlay}
            isMuted={cell.isMuted}
            hasAudio={capabilityAudioAvailable}
            volume={volume}
            onAudioStatusChange={setAudioStatus}
            onStatsChange={handlePlayerStatsChange}
            onFirstFrame={handleFirstFrame}
          />
        )}
      </div>

      {/* 状态与加载指示遮罩：连接中 / 重连中 / 已连上但首个画面还没出来 */}
      {waitingText !== null && (
        <div className='absolute inset-0 z-10 flex flex-col items-center justify-center bg-neutral-950/80 backdrop-blur-xs'>
          <div className='relative flex items-center justify-center'>
            <div className='absolute h-10 w-10 animate-ping rounded-full bg-primary/20' />
            <Loader2 className='relative h-6 w-6 animate-spin text-primary' />
          </div>
          <p className='mt-3 font-mono text-xs text-neutral-300 drop-shadow-xs'>
            {waitingText}
          </p>
        </div>
      )}

      {/* 异常错误提示遮罩 */}
      {!useMockCanvas && status === 'error' && (
        <div className='absolute inset-0 z-10 flex flex-col items-center justify-center border border-rose-500/20 bg-neutral-950/90 p-4 text-center backdrop-blur-xs'>
          <div className='rounded-full bg-rose-500/15 p-2.5 text-rose-400 shadow-sm'>
            <RotateCcw className='h-5 w-5' />
          </div>
          <p className='mt-2.5 text-xs font-medium text-rose-300'>
            {errorMessage || t('streamInterrupted')}
          </p>
          <Button
            size='sm'
            variant='outline'
            onClick={(e) => {
              e.stopPropagation()
              retry()
            }}
            className='mt-3 h-7 border-white/20 bg-white/10 text-xs text-white hover:bg-white/20 hover:text-white'
          >
            {t('reconnect')}
          </Button>
        </div>
      )}

      {/* 顶部悬浮控制岛 */}
      <LivePlayerCellHeader
        cell={cell}
        isMicro={isMicro}
        useMockCanvas={useMockCanvas}
        onToggleMock={() => setUseMockCanvas(!useMockCanvas)}
        onSetStreamType={onSetStreamType}
        onClear={onClear}
      />

      {/* 底部悬浮控制岛 */}
      <LivePlayerCellFooter
        cell={cell}
        stats={stats}
        isMicro={isMicro}
        isMaximized={isMaximized}
        useMockCanvas={useMockCanvas}
        onScreenshot={handleScreenshot}
        onToggleMute={handleToggleMute}
        onToggleMaximize={onToggleMaximize}
        audioDisabledReason={audioDisabledReason}
        audioDisabled={audioDisabled}
      />
    </div>
  )
})
