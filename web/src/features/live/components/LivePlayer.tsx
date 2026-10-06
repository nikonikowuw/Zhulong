import { AlertCircle, AlertTriangle, Loader2 } from 'lucide-react';
import React, { forwardRef, useEffect, useImperativeHandle, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { RoiBox } from '../core/types';
import { useLiveStream } from '../hooks/useLiveStream';
import { LiveTelemetryHud } from './LiveTelemetryHud';
import { RoiOverlayCanvas } from './RoiOverlayCanvas';

export interface LivePlayerHandle {
  takeSnapshot: () => Promise<boolean>;
}

export interface LivePlayerProps {
  cameraId: string;
  role?: 'main' | 'sub';
  boxes?: RoiBox[];
  showTelemetry?: boolean;
  onSnapshotReady?: (ready: boolean) => void;
}

export const LivePlayer = forwardRef<LivePlayerHandle, LivePlayerProps>(function LivePlayer(
  {
    cameraId,
    role = 'main',
    boxes = [],
    showTelemetry = true,
    onSnapshotReady,
  },
  ref,
) {
  const { t } = useTranslation();
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  const { status, telemetry, isSupported, hasFirstFrame, error } = useLiveStream({
    cameraId,
    role,
    canvasRef,
    enabled: true,
  });

  useEffect(() => {
    onSnapshotReady?.(hasFirstFrame);
  }, [hasFirstFrame, onSnapshotReady]);

  useImperativeHandle(
    ref,
    () => ({
      takeSnapshot: async () => {
        const canvas = canvasRef.current;
        if (!canvas || !hasFirstFrame) {
          return false;
        }
        try {
          const dataUrl = canvas.toDataURL('image/png');
          const a = document.createElement('a');
          const now = new Date();
          const pad = (n: number) => String(n).padStart(2, '0');
          const timestamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}_${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
          a.href = dataUrl;
          a.download = `snapshot_${cameraId}_${role}_${timestamp}.png`;
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          return true;
        } catch (e) {
          console.error('Failed to take snapshot:', e);
          return false;
        }
      },
    }),
    [cameraId, role, hasFirstFrame],
  );

  return (
    <div className="relative w-full h-full bg-neutral-950 flex items-center justify-center overflow-hidden select-none group">
      {/* 核心解码渲染画布 */}
      <canvas
        ref={canvasRef}
        className="max-w-full max-h-full object-contain pointer-events-none"
        width={1280}
        height={720}
      />

      {/* AI 目标检测框覆盖层 */}
      <RoiOverlayCanvas boxes={boxes} />

      {/* 实时遥测 HUD (右下角) */}
      <LiveTelemetryHud telemetry={telemetry} visible={showTelemetry && hasFirstFrame} />

      {/* 不支持 WebCodecs 时的能力降级遮罩 */}
      {!isSupported && (
        <div className="absolute inset-0 bg-neutral-900/90 backdrop-blur-md flex flex-col items-center justify-center p-6 text-center z-20">
          <AlertTriangle className="w-10 h-10 text-amber-400 mb-3" />
          <h4 className="text-sm font-semibold text-white mb-1">
            {t('live.unsupportedTitle', 'WebCodecs 不可用')}
          </h4>
          <p className="text-xs text-neutral-400 max-w-xs leading-relaxed">
            {t('live.unsupportedDesc', '当前浏览器未启用 WebCodecs 硬件加速或处于非安全上下文（需 HTTPS 或 localhost）。')}
          </p>
        </div>
      )}

      {/* 连接中与等待首帧 */}
      {isSupported && !hasFirstFrame && (status === 'connecting' || status === 'connected') && (
        <div className="absolute inset-0 bg-neutral-950/80 backdrop-blur-sm flex flex-col items-center justify-center gap-2.5 z-10">
          <Loader2 className="w-6 h-6 text-blue-500 animate-spin" />
          <span className="text-xs text-neutral-300 font-medium tracking-wide">
            {status === 'connecting'
              ? t('live.connecting', '正在连接 WebSocket 码流...')
              : t('live.awaitingFrame', '等待关键帧首包 (GOP Cache)...')}
          </span>
        </div>
      )}

      {/* 断线重连指示 */}
      {status === 'reconnecting' && (
        <div className="absolute top-3 left-3 z-20 flex items-center gap-2 px-3 py-1.5 rounded-full backdrop-blur-md bg-amber-500/20 border border-amber-500/30 text-amber-300 text-xs shadow-md">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          <span>{t('live.reconnecting', '正在尝试自动重连...')}</span>
        </div>
      )}

      {/* 错误提示 */}
      {status === 'error' && (
        <div className="absolute inset-0 bg-neutral-950/85 backdrop-blur-sm flex flex-col items-center justify-center gap-2 z-20">
          <AlertCircle className="w-8 h-8 text-rose-500" />
          <span className="text-xs text-rose-300 font-medium">
            {error || t('live.streamError', '码流拉取异常，请检查设备或网络')}
          </span>
        </div>
      )}
    </div>
  );
});
