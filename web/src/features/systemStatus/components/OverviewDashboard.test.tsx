import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OverviewDashboard } from "./OverviewDashboard";

describe("OverviewDashboard", () => {
  let queryClient: QueryClient;

  beforeEach(async () => {
    await i18n.changeLanguage("en");
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Infinity } },
    });
    vi.stubGlobal("fetch", vi.fn().mockImplementation((input: string | Request) => {
      const url = typeof input === "string" ? input : input.url;
      if (url.includes("/api/v1/cameras")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({
            code: "OK",
            message: "Success",
            data: [
              {
                id: "cam-1",
                name: "Cam 1",
                enabled: true,
                revision: 1,
                health: "online",
                session: "running",
                degraded: false,
                stale: false,
                streams: [],
                createdAt: "2026-10-06T00:00:00Z",
                updatedAt: "2026-10-06T00:00:00Z",
              },
            ],
          }),
        });
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => ({
          code: "OK",
          message: "Success",
          data: {
            status: "ready",
            components: { database: "ready", engine: "ready" },
          },
        }),
      });
    }));
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders metric cards and quick action triggers", async () => {
    const user = userEvent.setup();
    const onNavigateTab = vi.fn();

    render(
      <QueryClientProvider client={queryClient}>
        <OverviewDashboard onNavigateTab={onNavigateTab} />
      </QueryClientProvider>,
    );

    expect(screen.getByRole("heading", { name: "System Overview" })).toBeInTheDocument();
    expect(screen.getByText("Camera Assets")).toBeInTheDocument();
    expect(screen.getByText("Live Video Streams")).toBeInTheDocument();
    expect(screen.getByText("Inference Engine")).toBeInTheDocument();

    const quickLiveBtn = screen.getByRole("button", { name: "View Live Surveillance" });
    await user.click(quickLiveBtn);
    expect(onNavigateTab).toHaveBeenCalledWith("live");

    const quickCamerasBtn = screen.getByRole("button", { name: "Manage Cameras" });
    await user.click(quickCamerasBtn);
    expect(onNavigateTab).toHaveBeenCalledWith("cameras");
  });
});
