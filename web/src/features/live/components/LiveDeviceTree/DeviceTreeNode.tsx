import { Camera, GripVertical } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { CameraResponse } from '../../../camera/types';
import type { SlotBinding } from '../../core/types';
import { StreamTreeNode } from './StreamTreeNode';

interface DeviceTreeNodeProps {
  camera: CameraResponse;
  getPlayingSlot: (cameraId: string, role?: 'main' | 'sub') => number | null;
  onPlay: (binding: SlotBinding) => void;
}

export const DeviceTreeNode: React.FC<DeviceTreeNodeProps> = ({
  camera,
  getPlayingSlot,
  onPlay,
}) => {
  const { t } = useTranslation();

  const isOnline = camera.health === 'online';
  const isDegraded = camera.degraded || camera.health === 'error';
  const isPlayingAny = getPlayingSlot(camera.id) !== null;
  const hasStreams = camera.streams.length > 0;

  const targetStream =
    camera.streams.find((s) => s.role === 'main') || camera.streams[0];

  // 单击设备节点时，直接点播主码流至聚焦视口
  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (targetStream) {
      onPlay({
        cameraId: camera.id,
        role: targetStream.role,
        name: camera.name,
      });
    }
  };

  // 支持将整个设备（默认主码流）拖拽到目标视口
  const handleDragStart = (e: React.DragEvent) => {
    if (targetStream) {
      e.dataTransfer.setData(
        'application/json',
        JSON.stringify({
          cameraId: camera.id,
          role: targetStream.role,
          name: camera.name,
        }),
      );
      e.dataTransfer.effectAllowed = 'copy';
    }
  };

  const getStatusDotClass = () => {
    if (isDegraded) return 'bg-amber-500';
    if (isOnline) return 'bg-emerald-500';
    return 'bg-neutral-400 dark:bg-neutral-600';
  };

  return (
    <div className="flex flex-col mb-1.5 last:mb-0">
      {/* 设备一级节点行 */}
      <div
        draggable={hasStreams}
        onDragStart={handleDragStart}
        onClick={handleClick}
        className={`group/device flex items-center justify-between px-2 py-1.5 rounded-lg text-xs transition-all select-none ${
          hasStreams ? 'cursor-grab active:cursor-grabbing' : 'cursor-default'
        } ${
          isPlayingAny
            ? 'bg-neutral-100/90 dark:bg-neutral-800/80 font-medium'
            : 'hover:bg-neutral-100/70 dark:hover:bg-neutral-800/50'
        }`}
        title={
          hasStreams
            ? t('live.treeDeviceHint', '点击装载主码流至聚焦视口，或拖拽至指定分屏')
            : undefined
        }
      >
        <div className="flex items-center gap-1.5 min-w-0">
          {/* 拖拽手柄指示 */}
          {hasStreams && (
            <GripVertical className="w-3 h-3 text-neutral-300 dark:text-neutral-600 opacity-40 group-hover/device:opacity-100 shrink-0 transition-opacity" />
          )}

          {/* 摄像机图标与在线状态点 */}
          <div className="relative shrink-0">
            <Camera
              className={`w-3.5 h-3.5 ${
                isOnline
                  ? 'text-neutral-700 dark:text-neutral-200'
                  : 'text-neutral-400 dark:text-neutral-500'
              }`}
            />
            <span
              className={`absolute -top-0.5 -right-0.5 w-1.5 h-1.5 rounded-full ring-1 ring-white dark:ring-neutral-900 ${getStatusDotClass()}`}
            />
          </div>

          {/* 摄像机名称与 ID */}
          <span
            className="truncate text-neutral-800 dark:text-neutral-200 font-medium"
            title={`${camera.name} (${camera.id})`}
          >
            {camera.name}
          </span>
        </div>

        {/* 右侧流通道数 */}
        <div className="flex items-center gap-1 shrink-0 ml-1.5">
          <span className="text-[10px] text-neutral-400 font-mono">
            {camera.streams.length}
          </span>
        </div>
      </div>

      {/* 码流通道平铺展示（无需任何折叠按钮） */}
      <div className="flex flex-col gap-0.5 mt-0.5">
        {camera.streams.length > 0 ? (
          camera.streams.map((stream) => (
            <StreamTreeNode
              key={stream.id}
              camera={camera}
              stream={stream}
              playingSlot={getPlayingSlot(camera.id, stream.role)}
              onPlay={onPlay}
            />
          ))
        ) : (
          <div className="pl-6 py-1 text-[11px] text-neutral-400 dark:text-neutral-500 italic">
            {t('live.noStreams', '暂无可用码流')}
          </div>
        )}
      </div>
    </div>
  );
};
