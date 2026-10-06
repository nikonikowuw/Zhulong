import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DeleteConfirmModal } from "./DeleteConfirmModal";
import type { CameraResponse } from "../types";

describe("DeleteConfirmModal", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockCamera: CameraResponse = {
    id: "cam-to-delete",
    name: "Parking Lot",
    enabled: true,
    revision: 1,
    health: "offline",
    session: "idle",
    degraded: false,
    stale: false,
    streams: [],
    createdAt: "2026-10-06T00:00:00Z",
    updatedAt: "2026-10-06T00:00:00Z",
  };

  it("renders camera name in delete confirmation prompt and triggers confirm", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue({});
    const onClose = vi.fn();

    render(
      <DeleteConfirmModal
        isOpen={true}
        onClose={onClose}
        onConfirm={onConfirm}
        camera={mockCamera}
      />,
    );

    expect(screen.getByText(/Parking Lot/)).toBeInTheDocument();

    const deleteBtn = screen.getByRole("button", { name: "Delete" });
    await user.click(deleteBtn);

    expect(onConfirm).toHaveBeenCalledWith("cam-to-delete");
  });
});
