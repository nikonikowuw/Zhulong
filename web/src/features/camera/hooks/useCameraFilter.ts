import { useMemo, useState } from "react";
import {
  calculateCameraCounts,
  isCameraAbnormal,
  type CameraCounts,
} from "../utils/statusHelper";
import type { CameraResponse } from "../types";

export type CameraHealthFilter = "all" | "online" | "offline" | "abnormal";
export type CameraSortOption = "name_asc" | "name_desc" | "health_priority" | "updated_desc";

export interface UseCameraFilterOptions {
  cameras?: CameraResponse[];
  initialKeyword?: string;
  initialHealthFilter?: CameraHealthFilter;
  initialSortOption?: CameraSortOption;
}

export interface UseCameraFilterResult {
  keyword: string;
  setKeyword: (kw: string) => void;
  healthFilter: CameraHealthFilter;
  setHealthFilter: (filter: CameraHealthFilter) => void;
  sortOption: CameraSortOption;
  setSortOption: (sort: CameraSortOption) => void;
  filteredCameras: CameraResponse[];
  hasActiveFilters: boolean;
  clearFilters: () => void;
  counts: CameraCounts;
}

function getHealthWeight(camera: CameraResponse): number {
  if (!camera.enabled) return 0;
  if (isCameraAbnormal(camera)) return 3;
  if (camera.health === "online") return 2;
  if (camera.health === "offline") return 1;
  return 0;
}

export function useCameraFilter({
  cameras = [],
  initialKeyword = "",
  initialHealthFilter = "all",
  initialSortOption = "health_priority",
}: UseCameraFilterOptions = {}): UseCameraFilterResult {
  const [keyword, setKeyword] = useState(initialKeyword);
  const [healthFilter, setHealthFilter] = useState<CameraHealthFilter>(initialHealthFilter);
  const [sortOption, setSortOption] = useState<CameraSortOption>(initialSortOption);

  const counts = useMemo(() => calculateCameraCounts(cameras), [cameras]);

  const filteredCameras = useMemo(() => {
    const trimmedKw = keyword.trim().toLowerCase();

    const filtered = cameras.filter((camera) => {
      // 1. 关键字匹配（名称或 ID）
      if (trimmedKw) {
        const matchesName = camera.name.toLowerCase().includes(trimmedKw);
        const matchesId = camera.id.toLowerCase().includes(trimmedKw);
        if (!matchesName && !matchesId) {
          return false;
        }
      }

      // 2. 状态标签过滤
      if (healthFilter === "online") {
        return camera.enabled && camera.health === "online";
      }
      if (healthFilter === "offline") {
        return camera.enabled && camera.health === "offline";
      }
      if (healthFilter === "abnormal") {
        return camera.enabled && isCameraAbnormal(camera);
      }

      return true;
    });

    // 3. 排序处理
    return filtered.slice().sort((a, b) => {
      switch (sortOption) {
        case "name_asc":
          return a.name.localeCompare(b.name);
        case "name_desc":
          return b.name.localeCompare(a.name);
        case "updated_desc": {
          const timeA = new Date(a.updatedAt).getTime() || 0;
          const timeB = new Date(b.updatedAt).getTime() || 0;
          return timeB - timeA;
        }
        case "health_priority":
        default: {
          const weightDiff = getHealthWeight(b) - getHealthWeight(a);
          if (weightDiff !== 0) return weightDiff;
          return a.name.localeCompare(b.name);
        }
      }
    });
  }, [cameras, keyword, healthFilter, sortOption]);

  const hasActiveFilters = keyword.trim().length > 0 || healthFilter !== "all";

  function clearFilters() {
    setKeyword("");
    setHealthFilter("all");
  }

  return {
    keyword,
    setKeyword,
    healthFilter,
    setHealthFilter,
    sortOption,
    setSortOption,
    filteredCameras,
    hasActiveFilters,
    clearFilters,
    counts,
  };
}
