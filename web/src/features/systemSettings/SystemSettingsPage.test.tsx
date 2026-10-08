import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SystemSettingsPage } from "./SystemSettingsPage";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => {
      const translations: Record<string, string> = {
        "systemSettings.title": "System Settings",
        "systemSettings.description": "Configure host physical interfaces and watchdog.",
        "systemSettings.tabsLabel": "Settings tabs",
        "systemSettings.tabs.network": "Network Configuration",
        "systemSettings.network.metrics.interfacesCount": "interfaces",
        "systemSettings.network.actions.refresh": "Refresh",
      };
      return translations[key] ?? key;
    },
  }),
}));

vi.mock("./hooks/useNetwork", () => ({
  useNetworkInterfacesQuery: () => ({
    data: [
      {
        name: "eth0",
        mac: "52:54:00:12:34:56",
        linkUp: true,
        mode: "dhcp",
        ipAddresses: ["192.168.1.100/24"],
        gateway: "192.168.1.1",
        dns: ["8.8.8.8"],
        isDefaultGw: true,
        isCurrent: true,
      },
    ],
    isLoading: false,
    isRefetching: false,
    refetch: vi.fn(),
  }),
  useNetworkStatusQuery: () => ({
    data: null,
  }),
  useApplyNetworkConfigMutation: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useConfirmNetworkMutation: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useRollbackNetworkMutation: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
}));

describe("SystemSettingsPage", () => {
  it("renders page header and tabs consistently", () => {
    const queryClient = new QueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <SystemSettingsPage />
      </QueryClientProvider>,
    );

    expect(screen.getByRole("heading", { name: "System Settings", level: 1 })).toBeInTheDocument();
    expect(screen.getByText("Network Configuration")).toBeInTheDocument();
  });
});
