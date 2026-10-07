import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditLogFilterBar } from "./AuditLogFilterBar";

describe("AuditLogFilterBar", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("handles filtering and action buttons", async () => {
    const user = userEvent.setup();
    const onActionChange = vi.fn();
    const onStatusChange = vi.fn();
    const onRefresh = vi.fn();
    const onOpenClearModal = vi.fn();

    render(
      <AuditLogFilterBar
        action=""
        onActionChange={onActionChange}
        status=""
        onStatusChange={onStatusChange}
        onRefresh={onRefresh}
        isRefreshing={false}
        onOpenClearModal={onOpenClearModal}
      />,
    );

    // Select action
    const actionSelect = screen.getByLabelText("Action");
    await user.selectOptions(actionSelect, "auth.login");
    expect(onActionChange).toHaveBeenCalledWith("auth.login");

    // Select status
    const statusSelect = screen.getByLabelText("Status");
    await user.selectOptions(statusSelect, "success");
    expect(onStatusChange).toHaveBeenCalledWith("success");

    // Click refresh
    const refreshBtn = screen.getByTitle("Refresh");
    await user.click(refreshBtn);
    expect(onRefresh).toHaveBeenCalledTimes(1);

    // Click clear logs
    const clearBtn = screen.getByTitle("Clear Logs");
    await user.click(clearBtn);
    expect(onOpenClearModal).toHaveBeenCalledTimes(1);
  });
});
