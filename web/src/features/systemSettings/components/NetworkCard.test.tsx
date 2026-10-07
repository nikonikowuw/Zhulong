import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NetworkCard } from "./NetworkCard";
import type { InterfaceInfo } from "../types";

describe("NetworkCard", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  const mockInterface: InterfaceInfo = {
    name: "eth0",
    mac: "00:11:22:33:44:55",
    linkUp: true,
    mode: "static",
    ipAddresses: ["192.168.1.100/24"],
    gateway: "192.168.1.1",
    dns: ["8.8.8.8", "1.1.1.1"],
    isDefaultGw: true,
    isCurrent: true,
  };

  it("renders interface name, ip, gateway, and badges", () => {
    render(<NetworkCard iface={mockInterface} onEdit={vi.fn()} />);

    expect(screen.getByText("eth0")).toBeInTheDocument();
    expect(screen.getByText("192.168.1.100/24")).toBeInTheDocument();
    expect(screen.getByText("192.168.1.1")).toBeInTheDocument();
    expect(screen.getByText("8.8.8.8, 1.1.1.1")).toBeInTheDocument();
    expect(screen.getByText("Current Connection")).toBeInTheDocument();
    expect(screen.getByText("Default Gateway")).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Static IP")).toBeInTheDocument();
  });

  it("calls onEdit when Configure button is clicked", async () => {
    const user = userEvent.setup();
    const handleEdit = vi.fn();

    render(<NetworkCard iface={mockInterface} onEdit={handleEdit} />);

    const configureBtn = screen.getByRole("button", { name: /Configure/i });
    await user.click(configureBtn);

    expect(handleEdit).toHaveBeenCalledWith(mockInterface);
  });
});
