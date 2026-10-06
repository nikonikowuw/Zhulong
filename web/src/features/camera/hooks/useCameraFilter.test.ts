import { renderHook, act } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useCameraFilter } from "./useCameraFilter";
import type { CameraResponse } from "../types";

const mockCameras: CameraResponse[] = [
  {
    id: "cam-1",
    name: "Front Gate",
    enabled: true,
    revision: 1,
    health: "online",
    session: "running",
    streams: [],
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
  },
  {
    id: "cam-2",
    name: "Backyard",
    enabled: true,
    revision: 1,
    health: "offline",
    session: "idle",
    streams: [],
    createdAt: "2026-10-02T00:00:00Z",
    updatedAt: "2026-10-02T00:00:00Z",
  },
  {
    id: "cam-3",
    name: "Parking Lot",
    enabled: true,
    revision: 1,
    health: "error",
    session: "error",
    degraded: true,
    streams: [],
    createdAt: "2026-10-03T00:00:00Z",
    updatedAt: "2026-10-03T00:00:00Z",
  },
];

describe("useCameraFilter", () => {
  it("calculates correct initial counts", () => {
    const { result } = renderHook(() => useCameraFilter({ cameras: mockCameras }));

    expect(result.current.counts).toEqual({
      all: 3,
      online: 1,
      offline: 1,
      abnormal: 1,
    });
  });

  it("filters cameras by search keyword", () => {
    const { result } = renderHook(() => useCameraFilter({ cameras: mockCameras }));

    act(() => {
      result.current.setKeyword("Gate");
    });

    expect(result.current.filteredCameras).toHaveLength(1);
    expect(result.current.filteredCameras[0]?.id).toBe("cam-1");
    expect(result.current.hasActiveFilters).toBe(true);

    // Clear filters
    act(() => {
      result.current.clearFilters();
    });
    expect(result.current.keyword).toBe("");
    expect(result.current.filteredCameras).toHaveLength(3);
    expect(result.current.hasActiveFilters).toBe(false);
  });

  it("filters cameras by health filter", () => {
    const { result } = renderHook(() => useCameraFilter({ cameras: mockCameras }));

    act(() => {
      result.current.setHealthFilter("online");
    });
    expect(result.current.filteredCameras).toHaveLength(1);
    expect(result.current.filteredCameras[0]?.id).toBe("cam-1");

    act(() => {
      result.current.setHealthFilter("abnormal");
    });
    expect(result.current.filteredCameras).toHaveLength(1);
    expect(result.current.filteredCameras[0]?.id).toBe("cam-3");
  });

  it("sorts cameras by name ascending and descending", () => {
    const { result } = renderHook(() => useCameraFilter({ cameras: mockCameras }));

    act(() => {
      result.current.setSortOption("name_asc");
    });
    expect(result.current.filteredCameras.map((c) => c.name)).toEqual([
      "Backyard",
      "Front Gate",
      "Parking Lot",
    ]);

    act(() => {
      result.current.setSortOption("name_desc");
    });
    expect(result.current.filteredCameras.map((c) => c.name)).toEqual([
      "Parking Lot",
      "Front Gate",
      "Backyard",
    ]);
  });
});
