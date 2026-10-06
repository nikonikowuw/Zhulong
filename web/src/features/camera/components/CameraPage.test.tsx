import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CameraPage } from "./CameraPage";

describe("CameraPage", () => {
  let queryClient: QueryClient;

  beforeEach(async () => {
    await i18n.changeLanguage("en");
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: Infinity },
      },
    });

    vi.stubGlobal("EventSource", class {
      onopen = null;
      onerror = null;
      addEventListener = vi.fn();
      removeEventListener = vi.fn();
      close = vi.fn();
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mockCamera = {
    id: "cam-test-1",
    name: "Warehouse Gate",
    enabled: true,
    revision: 1,
    health: "online" as const,
    session: "idle" as const,
    degraded: false,
    stale: false,
    streams: [
      {
        id: 1,
        role: "main" as const,
        protocol: "rtsp",
        rtspUrl: "rtsp://admin:pass@192.168.1.100/main",
        transport: "tcp" as const,
        codec: "H.264",
        width: 1920,
        height: 1080,
        fps: 25,
        fpsString: "25/1",
        createdAt: "2026-10-06T00:00:00Z",
        updatedAt: "2026-10-06T00:00:00Z",
      },
    ],
    createdAt: "2026-10-06T00:00:00Z",
    updatedAt: "2026-10-06T00:00:00Z",
  };

  it("renders camera list and dashboard when data loads", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: [mockCamera],
      }),
    } as Response);

    render(
      <QueryClientProvider client={queryClient}>
        <CameraPage />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("Warehouse Gate")).toBeInTheDocument();
  });

  it("opens add camera modal when clicking add button", async () => {
    const user = userEvent.setup();
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: [],
      }),
    } as Response);

    render(
      <QueryClientProvider client={queryClient}>
        <CameraPage />
      </QueryClientProvider>,
    );

    const addButtons = await screen.findAllByRole("button", { name: /Add Camera/i });
    expect(addButtons.length).toBeGreaterThan(0);
    const firstAddButton = addButtons[0];
    if (firstAddButton) {
      await user.click(firstAddButton);
    }

    expect(screen.getByRole("heading", { name: "Add Camera" })).toBeInTheDocument();
  });
});
