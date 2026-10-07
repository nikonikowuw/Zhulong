import { useCallback, useMemo, useState } from 'react';
import type { CameraResponse } from '../../camera/types';

export interface UseDeviceTreeFilterOptions {
  cameras: CameraResponse[];
  defaultExpanded?: boolean;
}

export interface DeviceTreeStats {
  total: number;
  online: number;
  offline: number;
  degraded: number;
}

export function useDeviceTreeFilter({
  cameras,
  defaultExpanded = true,
}: UseDeviceTreeFilterOptions) {
  const [searchQuery, setSearchQuery] = useState('');
  const [manualExpanded, setManualExpanded] = useState<Record<string, boolean>>({});

  const normalizedQuery = searchQuery.trim().toLowerCase();

  const filteredCameras = useMemo(() => {
    if (!normalizedQuery) return cameras;

    return cameras.filter((camera) => {
      const matchName = camera.name.toLowerCase().includes(normalizedQuery);
      const matchId = camera.id.toLowerCase().includes(normalizedQuery);
      const matchStream = camera.streams.some(
        (s) =>
          s.role.toLowerCase().includes(normalizedQuery) ||
          s.codec.toLowerCase().includes(normalizedQuery),
      );
      return matchName || matchId || matchStream;
    });
  }, [cameras, normalizedQuery]);

  const isExpanded = useCallback(
    (cameraId: string): boolean => {
      if (normalizedQuery) return true;
      return manualExpanded[cameraId] ?? defaultExpanded;
    },
    [defaultExpanded, manualExpanded, normalizedQuery],
  );

  const toggleExpand = useCallback(
    (cameraId: string): void => {
      setManualExpanded((prev) => ({
        ...prev,
        [cameraId]: !(prev[cameraId] ?? defaultExpanded),
      }));
    },
    [defaultExpanded],
  );

  const expandAll = useCallback((): void => {
    const next: Record<string, boolean> = {};
    for (const c of cameras) {
      next[c.id] = true;
    }
    setManualExpanded(next);
  }, [cameras]);

  const collapseAll = useCallback((): void => {
    const next: Record<string, boolean> = {};
    for (const c of cameras) {
      next[c.id] = false;
    }
    setManualExpanded(next);
  }, [cameras]);

  const stats = useMemo<DeviceTreeStats>(() => {
    let online = 0;
    let offline = 0;
    let degraded = 0;

    for (const c of cameras) {
      if (c.health === 'online') online++;
      else if (c.health === 'offline') offline++;

      if (c.degraded || c.health === 'error') degraded++;
    }

    return { total: cameras.length, online, offline, degraded };
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
