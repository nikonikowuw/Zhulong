import {
  Activity,
  ArrowDownToLine,
  Camera,
  Check,
  Maximize2,
  Minimize2,
  Trash2,
  Tv,
} from 'lucide-react';
import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { SlotBinding } from '../core/types';
import { LivePlayer, type LivePlayerHandle } from './LivePlayer';

interface LiveViewportProps {
  slotIndex: number;
  binding: SlotBinding | null;
  isSelected?: boolean;
  isFullscreen?: boolean;
  onSelect?: (slotIndex: number) => void;
  onAssign: (binding: SlotBinding) => void;
  onClear: () => void;
  onToggleRole: () => void;
  onToggleFullscreen: () => void;
}

export function LiveViewport({
  slotIndex,
  binding,
  isFullscreen = false,
  onSelect,
  onAssign,
  onClear,
  onToggleRole,
  onToggleFullscreen,
}: LiveViewportProps): React.JSX.Element {
  const { t } = useTranslation();
  const [isSnapshotReady, setIsSnapshotReady] = useState(false);
  const [isSnapshotSaved, setIsSnapshotSaved] = useState(false);
  const [showTelemetry, setShowTelemetry] = useState(true);
  const [isDragOver, setIsDragOver] = useState(false);
  const playerRef = useRef<LivePlayerHandle | null>(null);

  const handleTakeSnapshot = async (): Promise<void> => {
    if (!playerRef.current || !isSnapshotReady) return;
    const ok = await playerRef.current.takeSnapshot();
    if (ok) {
      setIsSnapshotSaved(true);
      setTimeout(() => setIsSnapshotSaved(false), 1500);
    }
  };

  const handleDragOver = (e: React.DragEvent): void => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
    if (!isDragOver) {
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent): void => {
    e.preventDefault();
    setIsDragOver(false);
  };

  const handleDrop = (e: React.DragEvent): void => {
    e.preventDefault();
    setIsDragOver(false);
    const raw = e.dataTransfer.getData('application/json');
    if (!raw) return;

    try {
      const parsed = JSON.parse(raw) as Partial<SlotBinding>;
      if (parsed.cameraId && parsed.role) {
        onAssign(parsed as SlotBinding);
        onSelect?.(slotIndex);
      }
    } catch {
      // Ignore invalid drag payload
    }
  };

  const containerClasses = isFullscreen
    ? 'fixed inset-0 z-50 rounded-none border-none h-full bg-black flex items-center justify-center'
    : 'aspect-video rounded-xl shadow-xs bg-neutral-900 border border-neutral-800/80 hover:border-neutral-700/80 transition-all duration-200 cursor-pointer';

  const snapshotTitle = isSnapshotReady
    ? t('live.snapshot', '截取画面快照')
    : t('live.snapshotUnavailable', '等待首帧就绪后截图');

  const snapshotButtonClass = isSnapshotReady
    ? 'hover:bg-white/20 text-white/80 hover:text-white'
    : 'opacity-40 cursor-not-allowed text-white/40';

  const telemetryTitle = showTelemetry
    ? t('live.hideTelemetry', '隐藏遥测指标')
    : t('live.showTelemetry', '显示遥测指标');

  const telemetryButtonClass = showTelemetry
    ? 'text-blue-400 hover:bg-white/20'
    : 'text-white/40 hover:bg-white/20';

  const fullscreenTitle = isFullscreen
    ? t('live.exitFullscreen', '退出全屏')
    : t('live.fullscreen', '单视口全屏');

  return (
    <div
      onClick={() => onSelect?.(slotIndex)}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      className={`relative w-full ${containerClasses} overflow-hidden flex flex-col group`}
    >
      {/* 拖拽放置高亮遮罩 */}
      {isDragOver && (
        <div className="absolute inset-0 z-50 bg-blue-600/30 border-2 border-dashed border-blue-400 backdrop-blur-xs flex flex-col items-center justify-center gap-2 text-white pointer-events-none animate-in fade-in duration-150">
          <div className="w-10 h-10 rounded-full bg-blue-600 flex items-center justify-center shadow-lg">
            <ArrowDownToLine className="w-5 h-5 text-white animate-bounce" />
          </div>
          <span className="text-xs font-semibold tracking-wide drop-shadow-sm">
            {t('live.dropToPlay', '释放以此视口播放')}
          </span>
        </div>
      )}

      {binding ? (
        <>
          {/* 顶部悬浮控制栏 */}
          <div className="absolute top-2.5 inset-x-2.5 z-20 flex items-center justify-between pointer-events-none transition-opacity duration-200">
            {/* 左侧：视口编号、摄像机名称与主/子流切换 */}
            <div className="flex items-center gap-1.5 pointer-events-auto backdrop-blur-md bg-black/60 border border-white/10 px-2 py-1 rounded-full shadow-sm">
              <span
                className="text-[10px] font-mono font-bold px-1.5 py-0.2 rounded-md bg-white/20 text-white/90"
                title={t('live.slotIndexTitle', '视口 {{index}}', {
                  index: slotIndex + 1,
                })}
              >
                {slotIndex + 1}
              </span>
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              <span className="text-xs font-semibold text-white truncate max-w-[130px]">
                {binding.name || binding.cameraId}
              </span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onToggleRole();
                }}
                title={t('live.toggleRole', '点击切换主/子流')}
                className="text-[10px] font-mono uppercase px-1.5 py-0.5 rounded-md bg-white/15 hover:bg-white/25 text-white/90 transition-colors cursor-pointer"
              >
                {binding.role}
              </button>
            </div>

            {/* 右侧：截图、遥测、单槽位全屏与关闭按钮 */}
            <div className="flex items-center gap-1 pointer-events-auto backdrop-blur-md bg-black/60 border border-white/10 p-1 rounded-full shadow-sm">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  void handleTakeSnapshot();
                }}
                disabled={!isSnapshotReady}
                title={snapshotTitle}
                className={`p-1 rounded-full transition-colors cursor-pointer ${snapshotButtonClass}`}
              >
                {isSnapshotSaved ? (
                  <Check className="w-3.5 h-3.5 text-emerald-400" />
                ) : (
                  <Camera className="w-3.5 h-3.5" />
                )}
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  setShowTelemetry((prev) => !prev);
                }}
                title={telemetryTitle}
                className={`p-1 rounded-full transition-colors cursor-pointer ${telemetryButtonClass}`}
              >
                <Activity className="w-3.5 h-3.5" />
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onToggleFullscreen();
                }}
                title={fullscreenTitle}
                className="p-1 rounded-full hover:bg-white/20 text-white/80 hover:text-white transition-colors cursor-pointer"
              >
                {isFullscreen ? (
                  <Minimize2 className="w-3.5 h-3.5" />
                ) : (
                  <Maximize2 className="w-3.5 h-3.5" />
                )}
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onClear();
                }}
                title={t('live.removeSlot', '移除视口')}
                className="p-1 rounded-full hover:bg-rose-500/30 text-rose-300 hover:text-rose-200 transition-colors cursor-pointer"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            </div>
          </div>

          {/* 核心播放器 */}
          <LivePlayer
            ref={playerRef}
            cameraId={binding.cameraId}
            role={binding.role}
            showTelemetry={showTelemetry}
            onSnapshotReady={setIsSnapshotReady}
          />
        </>
      ) : (
        /* 空视口：纯净监控槽位展示，仅保留槽位号与拖拽接收 */
        <div className="w-full h-full flex flex-col items-center justify-center gap-2 text-neutral-500 hover:text-neutral-400 transition-all p-4 select-none bg-neutral-900/60">
          <div className="absolute top-2.5 left-2.5 flex items-center gap-1.5 pointer-events-none">
            <span className="text-[10px] font-mono font-bold px-1.5 py-0.2 rounded-md bg-white/10 text-white/50">
              {slotIndex + 1}
            </span>
            <span className="text-xs font-medium text-neutral-400">
              {t('live.emptySlot', '视口 {{index}}', {
                index: slotIndex + 1,
              })}
            </span>
          </div>

          <div className="w-11 h-11 rounded-2xl flex items-center justify-center bg-neutral-800/60 border border-neutral-700/40 text-neutral-500 transition-all">
            <Tv className="w-5 h-5" />
          </div>

          <div className="flex flex-col items-center gap-0.5 text-center">
            <span className="text-xs font-medium tracking-wide text-neutral-400">
              {t('live.slotVacant', '空闲视口')}
            </span>
            <span className="text-[11px] text-neutral-500">
              {t('live.emptySlotInstruction', '可从左侧直接拖入通道')}
            </span>
          </div>
        </div>
      )}
    </div>
  );
}
