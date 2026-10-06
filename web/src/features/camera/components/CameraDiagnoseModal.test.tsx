import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CameraDiagnoseModal } from "./CameraDiagnoseModal";
import type { CameraResponse, DiagnoseResponse } from "../types";

describe("CameraDiagnoseModal", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockCamera: CameraResponse = {
    id: "cam-diag",
    name: "North Entry",
    enabled: true,
    revision: 1,
    health: "online",
    session: "running",
    degraded: false,
    stale: false,
    streams: [],
    createdAt: "2026-10-06T00:00:00Z",
    updatedAt: "2026-10-06T00:00:00Z",
  };

  const mockResult: DiagnoseResponse = {
    cameraId: "cam-diag",
    message: "Probe successful",
    state: {
      cameraId: "cam-diag",
      enabled: true,
      revision: 1,
      health: "online",
      session: "running",
      degraded: false,
      stale: false,
      streams: {
        main: {
          role: "main",
          health: "online",
          session: "running",
          evidenceType: "rtsp_describe",
          consecutiveFailures: 0,
        },
      },
    },
  };

  it("renders diagnosis results and triggers re-diagnose", async () => {
    const user = userEvent.setup();
    const onReDiagnose = vi.fn();
    const onClose = vi.fn();

    render(
      <CameraDiagnoseModal
        isOpen={true}
        onClose={onClose}
        camera={mockCamera}
        result={mockResult}
        isLoading={false}
        onReDiagnose={onReDiagnose}
      />,
    );

    expect(screen.getByText("Camera Diagnostics")).toBeInTheDocument();
    expect(screen.getByText("Probe successful")).toBeInTheDocument();
    expect(screen.getByText("main")).toBeInTheDocument();

    const refreshBtn = screen.getByRole("button", { name: "Refresh" });
    await user.click(refreshBtn);
    expect(onReDiagnose).toHaveBeenCalled();
  });
});
