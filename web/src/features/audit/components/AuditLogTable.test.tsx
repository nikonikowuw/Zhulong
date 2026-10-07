import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditLogTable } from "./AuditLogTable";
import type { AuditLogItem } from "../types";

describe("AuditLogTable", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockLogs: AuditLogItem[] = [
    {
      id: 1,
      createdAt: "2026-10-06T10:00:00Z",
      ip: "192.168.1.50",
      username: "admin",
      action: "auth.login",
      target: "user:admin",
      detail: `{"method":"password"}`,
      status: "success",
      errorMsg: "",
    },
    {
      id: 2,
      createdAt: "2026-10-06T10:05:00Z",
      ip: "192.168.1.51",
      username: "admin",
      action: "camera.create",
      target: "camera:cam_1",
      detail: `{"name":"Doorway"}`,
      status: "failed",
      errorMsg: "Invalid RTSP URL",
    },
  ];

  it("renders audit log items with formatted action, status badges, and handles selection", async () => {
    const user = userEvent.setup();
    const onPageChange = vi.fn();
    const onPageSizeChange = vi.fn();
    const onSelectLog = vi.fn();

    render(
      <AuditLogTable
        items={mockLogs}
        isLoading={false}
        total={2}
        page={1}
        pageSize={20}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        onSelectLog={onSelectLog}
      />,
    );

    expect(screen.getByText("Admin Login")).toBeInTheDocument();
    expect(screen.getByText("Add Camera")).toBeInTheDocument();
    expect(screen.getByText("192.168.1.50")).toBeInTheDocument();
    expect(screen.getByText("camera:cam_1")).toBeInTheDocument();

    // Success and failed badges
    expect(screen.getByText("Success")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();

    // Click row
    await user.click(screen.getByText("Admin Login"));
    expect(onSelectLog).toHaveBeenCalledWith(mockLogs[0]);
  });

  it("renders empty state when there are no logs", () => {
    render(
      <AuditLogTable
        items={[]}
        isLoading={false}
        total={0}
        page={1}
        pageSize={20}
        onPageChange={vi.fn()}
        onPageSizeChange={vi.fn()}
        onSelectLog={vi.fn()}
      />,
    );

    expect(screen.getByText("No audit records found")).toBeInTheDocument();
  });

  it("renders loading state with localized text and spinner", () => {
    render(
      <AuditLogTable
        items={[]}
        isLoading={true}
        total={0}
        page={1}
        pageSize={20}
        onPageChange={vi.fn()}
        onPageSizeChange={vi.fn()}
        onSelectLog={vi.fn()}
      />,
    );

    expect(screen.getByText("Loading audit logs...")).toBeInTheDocument();
  });

  it("renders error state and triggers onRetry", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();

    render(
      <AuditLogTable
        items={[]}
        isLoading={false}
        error={new Error("Network connection lost")}
        onRetry={onRetry}
        total={0}
        page={1}
        pageSize={20}
        onPageChange={vi.fn()}
        onPageSizeChange={vi.fn()}
        onSelectLog={vi.fn()}
      />,
    );

    expect(screen.getByText("Network connection lost")).toBeInTheDocument();
    const retryBtn = screen.getByRole("button", { name: "Retry" });
    await user.click(retryBtn);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("handles pagination navigation", async () => {
    const user = userEvent.setup();
    const onPageChange = vi.fn();

    render(
      <AuditLogTable
        items={mockLogs}
        isLoading={false}
        total={50}
        page={1}
        pageSize={20}
        onPageChange={onPageChange}
        onPageSizeChange={vi.fn()}
        onSelectLog={vi.fn()}
      />,
    );

    expect(screen.getByText("Page 1 of 3")).toBeInTheDocument();
    const nextBtn = screen.getByTitle("Next");
    await user.click(nextBtn);
    expect(onPageChange).toHaveBeenCalledWith(2);
  });
});
