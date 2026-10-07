import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NetworkMigrationModal } from "./NetworkMigrationModal";
import type { ApplyResponse } from "../types";

describe("NetworkMigrationModal", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockMigration: ApplyResponse = {
    transactionId: "tx-123",
    timeoutSec: 60,
    targetUrl: "http://192.168.1.200:8080/#settings",
    confirmToken: "token-abc-xyz",
  };

  it("renders target url and countdown", () => {
    render(
      <NetworkMigrationModal
        migration={mockMigration}
        isOpen={true}
        onConfirm={vi.fn()}
        onRollback={vi.fn()}
        onClose={vi.fn()}
        isConfirming={false}
        isRollingBack={false}
      />,
    );

    expect(screen.getByText("Network Reconfiguration in Progress")).toBeInTheDocument();
    expect(screen.getByText("http://192.168.1.200:8080/#settings")).toBeInTheDocument();
    expect(screen.getByText(/60s remaining/)).toBeInTheDocument();
  });

  it("triggers onConfirm when Confirm & Save is clicked", async () => {
    const user = userEvent.setup();
    const handleConfirm = vi.fn();

    render(
      <NetworkMigrationModal
        migration={mockMigration}
        isOpen={true}
        onConfirm={handleConfirm}
        onRollback={vi.fn()}
        onClose={vi.fn()}
        isConfirming={false}
        isRollingBack={false}
      />,
    );

    const confirmBtn = screen.getByRole("button", { name: /Confirm & Save/i });
    await user.click(confirmBtn);

    expect(handleConfirm).toHaveBeenCalledWith("token-abc-xyz");
  });

  it("triggers onRollback when Rollback Now is clicked", async () => {
    const user = userEvent.setup();
    const handleRollback = vi.fn();

    render(
      <NetworkMigrationModal
        migration={mockMigration}
        isOpen={true}
        onConfirm={vi.fn()}
        onRollback={handleRollback}
        onClose={vi.fn()}
        isConfirming={false}
        isRollingBack={false}
      />,
    );

    const rollbackBtn = screen.getByRole("button", { name: /Rollback Now/i });
    await user.click(rollbackBtn);

    expect(handleRollback).toHaveBeenCalledWith("token-abc-xyz");
  });
});
