import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CameraCard } from "./CameraCard";
import type { CameraResponse } from "../types";

describe("CameraCard", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockCamera: CameraResponse = {
    id: "cam-101",
    name: "South Gate Camera",
    enabled: true,
    revision: 1,
    health: "online",
    session: "running",
    degraded: false,
    stale: false,
    streams: [
      {
        id: 1,
        role: "main",
        protocol: "rtsp",
        rtspUrl: "rtsp://admin:secret123@192.168.1.100:554/live",
        transport: "tcp",
        codec: "H.264",
        width: 1920,
        height: 1080,
        fps: 25,
        fpsString: "25/1",
        createdAt: "2026-10-06T00:00:00Z",
        updatedAt: "2026-10-06T00:00:00Z",
      },
    ],
    createdAt: "2026-10-06T00:00:00Z",
    updatedAt: "2026-10-06T00:00:00Z",
  };

  it("renders camera name and stream information", () => {
    render(
      <CameraCard
        camera={mockCamera}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onDiagnose={vi.fn()}
        onToggleEnabled={vi.fn()}
      />,
    );

    expect(screen.getByText("South Gate Camera")).toBeInTheDocument();
    expect(screen.getByText(/1920×1080/)).toBeInTheDocument();
  });

  it("masks password by default and reveals on eye toggle", async () => {
    const user = userEvent.setup();
    render(
      <CameraCard
        camera={mockCamera}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onDiagnose={vi.fn()}
        onToggleEnabled={vi.fn()}
      />,
    );

    // Default masked
    expect(screen.getByText(/rtsp:\/\/admin:••••••••@192.168.1.100:554\/live/)).toBeInTheDocument();

    // Click reveal password button
    const eyeBtn = screen.getByRole("button", { name: "Show password" });
    await user.click(eyeBtn);

    // Unmasked
    expect(screen.getByText("rtsp://admin:secret123@192.168.1.100:554/live")).toBeInTheDocument();
  });

  it("triggers actions on button clicks", async () => {
    const user = userEvent.setup();
    const onEdit = vi.fn();
    const onDelete = vi.fn();
    const onDiagnose = vi.fn();
    const onToggleEnabled = vi.fn();

    render(
      <CameraCard
        camera={mockCamera}
        onEdit={onEdit}
        onDelete={onDelete}
        onDiagnose={onDiagnose}
        onToggleEnabled={onToggleEnabled}
      />,
    );

    const editBtn = screen.getByRole("button", { name: "Edit" });
    await user.click(editBtn);
    expect(onEdit).toHaveBeenCalledWith(mockCamera);

    const deleteBtn = screen.getByRole("button", { name: "Delete" });
    await user.click(deleteBtn);
    expect(onDelete).toHaveBeenCalledWith(mockCamera);

    const diagnoseBtn = screen.getByRole("button", { name: "Diagnose" });
    await user.click(diagnoseBtn);
    expect(onDiagnose).toHaveBeenCalledWith(mockCamera);
  });
});
