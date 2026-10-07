import { GripVertical, Video } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { CameraResponse, StreamResponse } from '../../../camera/types';
import type { SlotBinding } from '../../core/types';

interface StreamTreeNodeProps {
  camera: CameraResponse;
  stream: StreamResponse;
  playingSlot: number | null;
  onPlay: (binding: SlotBinding) => void;
}

export const StreamTreeNode: React.FC<StreamTreeNodeProps> = ({
  camera,
  stream,
  playingSlot,
  onPlay,
}) => {
  const { t } = useTranslation();
  const isPlaying = playingSlot !== null;

  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    onPlay({
      cameraId: camera.id,
      role: stream.role,
      name: camera.name,
    });
  };

  const handleDragStart = (e: React.DragEvent) => {
    e.dataTransfer.setData(
      'application/json',
      JSON.stringify({
        cameraId: camera.id,
        role: stream.role,
        name: camera.name,
      }),
    );
    e.dataTransfer.effectAllowed = 'copy';
  };

  const roleLabel =
    stream.role === 'main'
      ? t('live.streamMain', '主码流')
      : t('live.streamSub', '子码流');

  const resolutionText =
    stream.width > 0 && stream.height > 0
      ? `${stream.width}×${stream.height}`
      : null;

  return (
    <div
      draggable
      onDragStart={handleDragStart}
      onClick={handleClick}
      className={`group/stream flex items-center justify-between pl-6 pr-2.5 py-1.5 rounded-lg text-xs transition-all cursor-grab active:cursor-grabbing select-none ${
        isPlaying
          ? 'bg-blue-50/80 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300 font-medium'
          : 'text-neutral-600 dark:text-neutral-400 hover:bg-neutral-100 dark:hover:bg-neutral-800/60 hover:text-neutral-900 dark:hover:text-neutral-200'
      }`}
      title={t('live.treeStreamHint', '点击装载至聚焦视口，或拖拽至指定分屏')}
    >
      <div className="flex items-center gap-1.5 min-w-0">
        <GripVertical className="w-3 h-3 text-neutral-300 dark:text-neutral-600 opacity-40 group-hover/stream:opacity-100 shrink-0 transition-opacity" />
        <Video
          className={`w-3.5 h-3.5 shrink-0 ${
            isPlaying ? 'text-blue-600 dark:text-blue-400' : 'text-neutral-400'
          }`}
        />
        <div className="flex items-center gap-1.5 truncate">
          <span className="truncate">{roleLabel}</span>
          <span
            className={`text-[10px] font-mono px-1 py-0.2 rounded uppercase ${
              stream.role === 'main'
                ? 'bg-blue-100 dark:bg-blue-900/40 text-blue-600 dark:text-blue-300'
                : 'bg-neutral-200/70 dark:bg-neutral-700/60 text-neutral-600 dark:text-neutral-400'
            }`}
          >
            {stream.role}
          </span>
          {resolutionText && (
            <span className="text-[10px] text-neutral-400 dark:text-neutral-500 font-mono hidden sm:inline">
              {resolutionText}
            </span>
          )}
        </div>
      </div>

      <div className="flex items-center gap-1.5 shrink-0 ml-2">
        {isPlaying ? (
          <span className="inline-flex items-center gap-1 text-[10px] font-mono font-medium px-1.5 py-0.5 rounded-md bg-emerald-100 dark:bg-emerald-950/50 text-emerald-700 dark:text-emerald-400 border border-emerald-300/60 dark:border-emerald-800/40 shadow-2xs">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
            <span>
              {t('live.playingInSlot', '视口 {{index}}', {
                index: playingSlot + 1,
              })}
            </span>
          </span>
        ) : null}
      </div>
    </div>
  );
};
