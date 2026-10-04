import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HealthPanel } from "@/features/systemStatus";

const readyResponse = {
  code: "OK",
  message: "Success",
  data: {
    status: "ready",
    components: { database: "ready", engine: "ready" },
  },
};

function response(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  };
}

function renderHealthPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <HealthPanel />
    </QueryClientProvider>,
  );
}

describe("HealthPanel", () => {
  beforeEach(async () => {
    localStorage.clear();
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows the loading and ready states from the health API", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(readyResponse)));
    renderHealthPanel();

    expect(screen.getByRole("status")).toHaveTextContent("Connecting to the local health endpoint");
    expect(await screen.findByRole("heading", { name: "All components are ready" })).toBeInTheDocument();
    expect(screen.getByText("Local database")).toBeInTheDocument();
    expect(screen.getByText("Native engine stub")).toBeInTheDocument();
  });

  it("shows a recoverable error and retries on user request", async () => {
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new TypeError("network detail must not be shown"))
      .mockResolvedValueOnce(response(readyResponse));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderHealthPanel();

    expect(await screen.findByRole("alert")).toHaveTextContent("The local health check did not complete");
    expect(screen.queryByText("network detail must not be shown")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry check" }));
    expect(await screen.findByRole("heading", { name: "All components are ready" })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
