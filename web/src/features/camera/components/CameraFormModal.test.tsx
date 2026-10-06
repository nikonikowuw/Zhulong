import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CameraFormModal } from "./CameraFormModal";

describe("CameraFormModal", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("renders create form fields and validates inputs", async () => {
    const user = userEvent.setup();
    const onSubmitCreate = vi.fn().mockResolvedValue({});
    const onSubmitUpdate = vi.fn().mockResolvedValue({});
    const onClose = vi.fn();

    render(
      <CameraFormModal
        isOpen={true}
        onClose={onClose}
        onSubmitCreate={onSubmitCreate}
        onSubmitUpdate={onSubmitUpdate}
        editingCamera={null}
      />,
    );

    expect(screen.getByRole("heading", { name: "Add Camera" })).toBeInTheDocument();

    const nameInput = screen.getByLabelText(/Camera Name/i);
    const urlInput = screen.getByPlaceholderText(/rtsp:\/\/username:password@ip:554\/live/i);

    await user.type(nameInput, "New Camera 4K");
    await user.type(urlInput, "rtsp://admin:pass@192.168.1.50/stream");

    const submitBtn = screen.getByRole("button", { name: "Probe & Save" });
    await user.click(submitBtn);

    expect(onSubmitCreate).toHaveBeenCalledWith({
      name: "New Camera 4K",
      enabled: true,
      mainStream: {
        role: "main",
        rtspUrl: "rtsp://admin:pass@192.168.1.50/stream",
        transport: "tcp",
      },
      subStream: undefined,
    });
  });

  it("renders edit form pre-filled with camera values", () => {
    const editingCamera = {
      id: "cam-1",
      name: "Front Door",
      enabled: true,
      revision: 2,
      health: "online" as const,
      session: "running" as const,
      degraded: false,
      stale: false,
      streams: [
        {
          id: 1,
          role: "main" as const,
          protocol: "rtsp",
          rtspUrl: "rtsp://admin:123@192.168.1.200/main",
          transport: "tcp" as const,
          codec: "H.264",
          width: 1920,
          height: 1080,
          fps: 30,
          fpsString: "30/1",
          createdAt: "2026-10-06T00:00:00Z",
          updatedAt: "2026-10-06T00:00:00Z",
        },
      ],
      createdAt: "2026-10-06T00:00:00Z",
      updatedAt: "2026-10-06T00:00:00Z",
    };

    render(
      <CameraFormModal
        isOpen={true}
        onClose={vi.fn()}
        onSubmitCreate={vi.fn()}
        onSubmitUpdate={vi.fn()}
        editingCamera={editingCamera}
      />,
    );

    expect(screen.getByRole("heading", { name: "Edit Camera" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("Front Door")).toBeInTheDocument();
    expect(screen.getByDisplayValue("rtsp://admin:123@192.168.1.200/main")).toBeInTheDocument();
  });
});
