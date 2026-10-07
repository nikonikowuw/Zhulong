import { AlertCircle, Film, RefreshCw, Search, X } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { useCamerasQuery } from '../../../camera/hooks/useCameras';
import type { SlotBinding } from '../../core/types';
import { useDeviceTreeFilter } from '../../hooks/useDeviceTreeFilter';
import { DeviceTreeNode } from './DeviceTreeNode';

interface LiveDeviceTreePanelProps {
  getPlayingSlot: (cameraId: string, role?: 'main' | 'sub') => number | null;
  onPlay: (binding: SlotBinding) => void;
}

export const LiveDeviceTreePanel: React.FC<LiveDeviceTreePanelProps> = ({
  getPlayingSlot,
  onPlay,
}) => {
  const { t } = useTranslation();
  const {
    data: cameras = [],
    isLoading,
    isError,
    refetch,
  } = useCamerasQuery();

  const {
    searchQuery,
    setSearchQuery,
    filteredCameras,
    stats,
  } = useDeviceTreeFilter({ cameras });

  return (
    <div className="flex flex-col h-full bg-white dark:bg-neutral-900 border border-neutral-200/80 dark:border-neutral-800 rounded-2xl shadow-xs overflow-hidden select-none">
      {/* 头部标题与在线统计（纯净无折叠按钮） */}
      <div className="flex items-center justify-between px-3.5 py-3 border-b border-neutral-100 dark:border-neutral-800/80">
        <div className="flex items-center gap-2 min-w-0">
          <Film className="w-4 h-4 text-blue-500 shrink-0" />
          <span className="text-xs font-semibold text-neutral-900 dark:text-neutral-100 truncate">
            {t('live.deviceTree', '媒体设备树')}
          </span>
        </div>
        <span className="text-[10px] font-mono px-1.5 py-0.2 rounded-full bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400 border border-emerald-200/60 dark:border-emerald-800/60 shrink-0">
          {stats.online}/{stats.total}
        </span>
      </div>

      {/* 快捷搜索栏 */}
      <div className="p-2 border-b border-neutral-100 dark:border-neutral-800/80">
        <div className="relative">
          <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-neutral-400" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t('live.searchDevices', '搜索设备或通道...')}
            className="w-full pl-8 pr-7 py-1.5 text-xs bg-neutral-50 dark:bg-neutral-800/60 border border-neutral-200/80 dark:border-neutral-700/60 rounded-xl focus:outline-hidden focus:ring-1 focus:ring-blue-500 text-neutral-800 dark:text-neutral-200 placeholder:text-neutral-400"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery('')}
              title={t('live.clearSearch', '清除搜索')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 cursor-pointer"
            >
              <X className="w-3 h-3" />
            </button>
          )}
        </div>
      </div>

      {/* 设备与通道列表主体 */}
      <div className="flex-1 overflow-y-auto p-2 min-h-0">
        {isLoading ? (
          <div className="flex flex-col items-center justify-center h-48 gap-2 text-neutral-400">
            <RefreshCw className="w-5 h-5 animate-spin text-blue-500" />
            <span className="text-xs">
              {t('live.connecting', '正在加载通道...')}
            </span>
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center justify-center h-48 gap-2 p-4 text-center">
            <AlertCircle className="w-6 h-6 text-rose-500" />
            <span className="text-xs text-neutral-600 dark:text-neutral-400">
              {t('live.loadDevicesFailed', '获取设备列表失败')}
            </span>
            <button
              type="button"
              onClick={() => void refetch()}
              className="mt-1 px-3 py-1 text-xs font-medium bg-neutral-100 hover:bg-neutral-200 dark:bg-neutral-800 dark:hover:bg-neutral-700 rounded-lg text-neutral-700 dark:text-neutral-300 transition-colors cursor-pointer"
            >
              {t('common.retry', '重试')}
            </button>
          </div>
        ) : filteredCameras.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-48 gap-1.5 text-neutral-400 dark:text-neutral-500 p-4 text-center">
            <span className="text-xs font-medium">
              {searchQuery
                ? t('live.noMatchingDevices', '未找到匹配的设备或通道')
                : t('live.noDevicesConfigured', '暂无已配置的摄像机设备')}
            </span>
          </div>
        ) : (
          <div className="flex flex-col">
            {filteredCameras.map((camera) => (
              <DeviceTreeNode
                key={camera.id}
                camera={camera}
                getPlayingSlot={getPlayingSlot}
                onPlay={onPlay}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
