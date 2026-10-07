import { useMemo, useState } from 'react';
import type { CameraResponse } from '../../camera/types';

export interface UseDeviceTreeFilterOptions {
  cameras: CameraResponse[];
  defaultExpanded?: boolean;
}

export function useDeviceTreeFilter({
  cameras,
  defaultExpanded = true,
}: UseDeviceTreeFilterOptions) {
  const [searchQuery, setSearchQuery] = useState('');
  const [manualExpanded, setManualExpanded] = useState<Record<string, boolean>>({});

  const normalizedQuery = searchQuery.trim().toLowerCase();

  // 1. 过滤匹配摄像机
  const filteredCameras = useMemo(() => {
    if (!normalizedQuery) return cameras;

    return cameras.filter((camera) => {
      const nameMatch = camera.name.toLowerCase().includes(normalizedQuery);
      const idMatch = camera.id.toLowerCase().includes(normalizedQuery);
      const streamMatch = camera.streams.some(
        (s) =>
          s.role.toLowerCase().includes(normalizedQuery) ||
          s.codec.toLowerCase().includes(normalizedQuery),
      );
      return nameMatch || idMatch || streamMatch;
    });
  }, [cameras, normalizedQuery]);

  // 2. 派生展开节点集合
  const isExpanded = (cameraId: string): boolean => {
    // 搜索匹配时强制展开以展示通道
    if (normalizedQuery) return true;
    if (cameraId in manualExpanded) {
      return !!manualExpanded[cameraId];
    }
    return defaultExpanded;
  };

  const toggleExpand = (cameraId: string) => {
    setManualExpanded((prev) => {
      const current = cameraId in prev ? prev[cameraId] : defaultExpanded;
      return {
        ...prev,
        [cameraId]: !current,
      };
    });
  };

  const expandAll = () => {
    const next: Record<string, boolean> = {};
    for (const c of cameras) {
      next[c.id] = true;
    }
    setManualExpanded(next);
  };

  const collapseAll = () => {
    const next: Record<string, boolean> = {};
    for (const c of cameras) {
      next[c.id] = false;
    }
    setManualExpanded(next);
  };

  // 3. 资产健康统计指标派生
  const stats = useMemo(() => {
    let online = 0;
    let offline = 0;
    let degraded = 0;

    for (const c of cameras) {
      if (c.health === 'online') {
        online++;
      } else if (c.health === 'offline') {
        offline++;
      }
      if (c.degraded || c.health === 'error') {
        degraded++;
      }
    }

    return {
      total: cameras.length,
      online,
      offline,
      degraded,
    };
  }, [cameras]);

  return {
    searchQuery,
    setSearchQuery,
    filteredCameras,
    isExpanded,
    toggleExpand,
    expandAll,
    collapseAll,
    stats,
  };
}
