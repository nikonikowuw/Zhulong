import React from 'react';
import { useLiveLayout } from '../hooks/useLiveLayout';
import { LiveDashboard } from './LiveDashboard';
import { LiveDeviceTreePanel } from './LiveDeviceTree/LiveDeviceTreePanel';

export const LivePage: React.FC = () => {
  const layout = useLiveLayout();

  return (
    <div className="w-full flex-1 min-h-0 flex flex-col lg:flex-row items-stretch gap-4 animate-in fade-in duration-200">
      {/* 左侧：媒体设备树面板（固定停靠、垂直撑满） */}
      <div className="w-full lg:w-64 xl:w-72 shrink-0 flex flex-col self-stretch">
        <LiveDeviceTreePanel
          getPlayingSlot={layout.getPlayingSlot}
          onPlay={layout.assignToSelected}
        />
      </div>

      {/* 右侧：多分屏监控工作区 */}
      <div className="flex-1 min-w-0 w-full flex flex-col self-stretch min-h-0">
        <LiveDashboard layout={layout} />
      </div>
    </div>
  );
};
