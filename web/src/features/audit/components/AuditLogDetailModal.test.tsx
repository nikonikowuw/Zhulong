import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditLogDetailModal } from "./AuditLogDetailModal";
import type { AuditLogItem } from "../types";

describe("AuditLogDetailModal", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockLog: AuditLogItem = {
    id: 42,
    createdAt: "2026-10-06T15:30:00Z",
    ip: "10.0.0.5",
    username: "admin",
    action: "camera.create",
    target: "camera:cam_test",
    detail: JSON.stringify({ name: "Entrance", protocol: "rtsp" }),
    status: "success",
    errorMsg: "",
  };

  it("renders formatted log details and triggers onClose", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <AuditLogDetailModal
        isOpen={true}
        onClose={onClose}
        log={mockLog}
      />,
    );

    expect(screen.getByText("#42")).toBeInTheDocument();
    expect(screen.getByText("camera.create")).toBeInTheDocument();
    expect(screen.getByText("camera:cam_test")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.5")).toBeInTheDocument();
    expect(screen.getByText(/"Entrance"/)).toBeInTheDocument();

    const closeBtns = screen.getAllByRole("button", { name: "Close" });
    await user.click(closeBtns[0]!);
    expect(onClose).toHaveBeenCalledTimes(1);

    // Escape key closes modal
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("does not render when isOpen is false", () => {
    const { container } = render(
      <AuditLogDetailModal
        isOpen={false}
        onClose={vi.fn()}
        log={mockLog}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
