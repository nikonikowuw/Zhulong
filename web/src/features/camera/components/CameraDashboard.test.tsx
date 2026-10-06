import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { CameraDashboard } from "./CameraDashboard";
import type { CameraResponse } from "../types";

describe("CameraDashboard", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockCameras: CameraResponse[] = [
    {
      id: "cam-1",
      name: "Camera 1",
      enabled: true,
      revision: 1,
      health: "online",
      session: "running",
      degraded: false,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
    {
      id: "cam-2",
      name: "Camera 2",
      enabled: true,
      revision: 1,
      health: "online",
      session: "running",
      degraded: false,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
    {
      id: "cam-3",
      name: "Camera 3",
      enabled: true,
      revision: 1,
      health: "online",
      session: "running",
      degraded: false,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
    {
      id: "cam-4",
      name: "Camera 4",
      enabled: true,
      revision: 1,
      health: "offline",
      session: "idle",
      degraded: false,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
    {
      id: "cam-5",
      name: "Camera 5",
      enabled: true,
      revision: 1,
      health: "error",
      session: "error",
      degraded: false,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
    {
      id: "cam-6",
      name: "Camera 6",
      enabled: true,
      revision: 1,
      health: "online",
      session: "running",
      degraded: true,
      stale: false,
      streams: [],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    },
  ];

  it("calculates and displays correct metric counts", () => {
    render(<CameraDashboard cameras={mockCameras} />);

    // Total: 6
    expect(screen.getByText("6")).toBeInTheDocument();
    // Online: 4
    expect(screen.getByText("4")).toBeInTheDocument();
    // Offline: 1
    expect(screen.getByText("1")).toBeInTheDocument();
    // Abnormal: 2 (error + degraded)
    expect(screen.getByText("2")).toBeInTheDocument();
  });
});
