import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WatchdogCountdownBanner } from "./WatchdogCountdownBanner";
import type { TransactionState } from "../types";

describe("WatchdogCountdownBanner", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockTransaction: NonNullable<TransactionState> = {
    transactionId: "tx-456",
    status: "pending_confirm",
    interfaceName: "eth0",
    confirmToken: "token-test",
    targetUrl: "http://10.0.0.1:8080",
    timeoutSec: 60,
    expiresAt: new Date(Date.now() + 50000).toISOString(),
    rollbackConfig: {
      mode: "dhcp",
      ipAddress: "",
      subnetMask: "",
      gateway: "",
      dns: [],
      setDefault: false,
    },
  };

  it("renders active banner with interface name and buttons", () => {
    render(
      <WatchdogCountdownBanner
        transaction={mockTransaction}
        onConfirm={vi.fn()}
        onRollback={vi.fn()}
        isConfirming={false}
        isRollingBack={false}
      />,
    );

    expect(screen.getByText(/Network Trial Active/i)).toBeInTheDocument();
    expect(screen.getByText(/eth0/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Confirm & Save/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Rollback Now/i })).toBeInTheDocument();
  });

  it("calls onConfirm when confirm is clicked", async () => {
    const user = userEvent.setup();
    const handleConfirm = vi.fn();

    render(
      <WatchdogCountdownBanner
        transaction={mockTransaction}
        onConfirm={handleConfirm}
        onRollback={vi.fn()}
        isConfirming={false}
        isRollingBack={false}
      />,
    );

    await user.click(screen.getByRole("button", { name: /Confirm & Save/i }));
    expect(handleConfirm).toHaveBeenCalledWith("token-test");
  });
});
