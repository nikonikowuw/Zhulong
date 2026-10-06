import { Activity, Camera, Check, Maximize2, Minimize2, Plus, SlidersHorizontal, Trash2 } from 'lucide-react';
import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { SlotBinding } from '../core/types';
import { LiveAssignModal } from './LiveAssignModal';
import { LivePlayer, type LivePlayerHandle } from './LivePlayer';

interface LiveViewportProps {
  slotIndex: number;
  binding: SlotBinding | null;
  isFullscreen?: boolean;
  onAssign: (binding: SlotBinding) => void;
  onClear: () => void;
  onToggleRole: () => void;
  onToggleFullscreen: () => void;
}

export const LiveViewport: React.FC<LiveViewportProps> = ({
  slotIndex,
  binding,
  isFullscreen = false,
  onAssign,
  onClear,
  onToggleRole,
  onToggleFullscreen,
}) => {
  const { t } = useTranslation();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isSnapshotReady, setIsSnapshotReady] = useState(false);
  const [isSnapshotSaved, setIsSnapshotSaved] = useState(false);
  const [showTelemetry, setShowTelemetry] = useState(true);
  const playerRef = useRef<LivePlayerHandle | null>(null);

  const handleTakeSnapshot = async () => {
    if (!playerRef.current || !isSnapshotReady) return;
    const ok = await playerRef.current.takeSnapshot();
    if (ok) {
      setIsSnapshotSaved(true);
      setTimeout(() => setIsSnapshotSaved(false), 1500);
    }
  };

  return (
    <div
      className={`relative w-full ${
        isFullscreen
          ? 'fixed inset-0 z-50 rounded-none border-none h-full bg-black flex items-center justify-center'
          : 'aspect-video rounded-xl border border-neutral-800/80 shadow-xs bg-neutral-900'
      } overflow-hidden flex flex-col group transition-all duration-300`}
    >
      {binding ? (
        <>
          {/* 顶部悬浮控制栏 */}
          <div className="absolute top-2.5 inset-x-2.5 z-20 flex items-center justify-between pointer-events-none transition-opacity duration-200">
            {/* 左侧：摄像机名称与主/子流切换 */}
            <div className="flex items-center gap-2 pointer-events-auto backdrop-blur-md bg-black/60 border border-white/10 px-2.5 py-1 rounded-full shadow-sm">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              <span className="text-xs font-semibold text-white truncate max-w-[140px]">
                {binding.name || binding.cameraId}
              </span>
              <button
                type="button"
                onClick={onToggleRole}
                title={t('live.toggleRole', '点击切换主/子流')}
                className="text-[10px] font-mono uppercase px-1.5 py-0.5 rounded-md bg-white/15 hover:bg-white/25 text-white/90 transition-colors cursor-pointer"
              >
                {binding.role}
              </button>
            </div>

            {/* 右侧：截图、遥测、更换、单槽位全屏与关闭按钮 */}
            <div className="flex items-center gap-1 pointer-events-auto backdrop-blur-md bg-black/60 border border-white/10 p-1 rounded-full shadow-sm">
              <button
                type="button"
                onClick={() => void handleTakeSnapshot()}
                disabled={!isSnapshotReady}
                title={isSnapshotReady ? t('live.snapshot', '截取画面快照') : t('live.snapshotUnavailable', '等待首帧就绪后截图')}
                className={`p-1 rounded-full transition-colors cursor-pointer ${
                  isSnapshotReady
                    ? 'hover:bg-white/20 text-white/80 hover:text-white'
                    : 'opacity-40 cursor-not-allowed text-white/40'
                }`}
              >
                {isSnapshotSaved ? (
                  <Check className="w-3.5 h-3.5 text-emerald-400" />
                ) : (
                  <Camera className="w-3.5 h-3.5" />
                )}
              </button>
              <button
                type="button"
                onClick={() => setShowTelemetry((prev) => !prev)}
                title={showTelemetry ? t('live.hideTelemetry', '隐藏遥测指标') : t('live.showTelemetry', '显示遥测指标')}
                className={`p-1 rounded-full transition-colors cursor-pointer ${
                  showTelemetry ? 'text-blue-400 hover:bg-white/20' : 'text-white/40 hover:bg-white/20'
                }`}
              >
                <Activity className="w-3.5 h-3.5" />
              </button>
              <button
                type="button"
                onClick={() => setIsModalOpen(true)}
                title={t('live.changeCamera', '更换摄像机')}
                className="p-1 rounded-full hover:bg-white/20 text-white/80 hover:text-white transition-colors cursor-pointer"
              >
                <SlidersHorizontal className="w-3.5 h-3.5" />
              </button>
              <button
                type="button"
                onClick={onToggleFullscreen}
                title={isFullscreen ? t('live.exitFullscreen', '退出全屏') : t('live.fullscreen', '单视口全屏')}
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
                onClick={onClear}
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
        /* 空视口引导占位 */
        <button
          type="button"
          onClick={() => setIsModalOpen(true)}
          className="w-full h-full flex flex-col items-center justify-center gap-3 text-neutral-400 hover:text-neutral-200 hover:bg-neutral-800/40 transition-all cursor-pointer p-6"
        >
          <div className="w-12 h-12 rounded-2xl bg-neutral-800 border border-neutral-700/60 flex items-center justify-center text-neutral-400 group-hover:scale-105 group-hover:text-blue-400 group-hover:border-blue-500/40 transition-all shadow-inner">
            <Plus className="w-6 h-6" />
          </div>
          <div className="flex flex-col items-center gap-0.5">
            <span className="text-xs font-semibold tracking-wide">
              {t('live.emptySlot', '视口 {{index}} - 点击分配', { index: slotIndex + 1 })}
            </span>
            <span className="text-[11px] text-neutral-500">
              {t('live.emptySlotHint', '分配 RTSP 实时码流')}
            </span>
          </div>
        </button>
      )}

      {/* 分配/更换弹窗 */}
      <LiveAssignModal
        isOpen={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        currentCameraId={binding?.cameraId}
        onAssign={(cameraId, role, name) => {
          onAssign({ cameraId, role, name });
        }}
      />
    </div>
  );
};
