import i18n from "@/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NetworkEditModal } from "./NetworkEditModal";
import type { InterfaceInfo } from "../types";

describe("NetworkEditModal", () => {
  let queryClient: QueryClient;

  beforeEach(async () => {
    await i18n.changeLanguage("en");
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
  });

  afterEach(() => {
    cleanup();
  });

  const mockIface: InterfaceInfo = {
    name: "eth0",
    mac: "00:11:22:33:44:55",
    linkUp: true,
    mode: "dhcp",
    ipAddresses: ["192.168.1.100/24"],
    gateway: "192.168.1.1",
    dns: ["8.8.8.8"],
    isDefaultGw: false,
    isCurrent: false,
  };

  it("renders DHCP mode and switches to Static mode showing inputs", async () => {
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={queryClient}>
        <NetworkEditModal
          iface={mockIface}
          allInterfaces={[mockIface]}
          isOpen={true}
          onClose={vi.fn()}
          onApply={vi.fn()}
          isApplying={false}
        />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Configure: eth0")).toBeInTheDocument();

    const staticBtn = screen.getByRole("button", { name: /Static IP/i });
    await user.click(staticBtn);

    expect(screen.getByPlaceholderText("192.168.1.100")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("255.255.255.0")).toBeInTheDocument();
  });

  it("validates invalid IP address on static submit", async () => {
    const user = userEvent.setup();
    const handleApply = vi.fn();

    render(
      <QueryClientProvider client={queryClient}>
        <NetworkEditModal
          iface={mockIface}
          allInterfaces={[mockIface]}
          isOpen={true}
          onClose={vi.fn()}
          onApply={handleApply}
          isApplying={false}
        />
      </QueryClientProvider>,
    );

    const staticBtn = screen.getByRole("button", { name: /Static IP/i });
    await user.click(staticBtn);

    const ipInput = screen.getByPlaceholderText("192.168.1.100");
    await user.clear(ipInput);
    await user.type(ipInput, "999.999.999.999");

    const submitBtn = screen.getByRole("button", { name: /Apply Settings/i });
    await user.click(submitBtn);

    expect(screen.getByText(/Invalid IPv4 address format/i)).toBeInTheDocument();
    expect(handleApply).not.toHaveBeenCalled();
  });

  it("validates invalid IP address on blur", async () => {
    const user = userEvent.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <NetworkEditModal
          iface={mockIface}
          allInterfaces={[mockIface]}
          isOpen={true}
          onClose={vi.fn()}
          onApply={vi.fn()}
          isApplying={false}
        />
      </QueryClientProvider>,
    );

    const staticBtn = screen.getByRole("button", { name: /Static IP/i });
    await user.click(staticBtn);

    const ipInput = screen.getByPlaceholderText("192.168.1.100");
    await user.clear(ipInput);
    await user.type(ipInput, "192.168.abc.1");
    await user.tab();

    expect(screen.getByText(/Invalid IPv4 address format/i)).toBeInTheDocument();
  });
});
