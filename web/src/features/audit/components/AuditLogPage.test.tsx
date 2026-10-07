import i18n from "@/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuditLogPage } from "./AuditLogPage";

function renderWithClient(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      {ui}
    </QueryClientProvider>,
  );
}

describe("AuditLogPage", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders page header and handles clear modal dialog", async () => {
    const user = userEvent.setup();

    // Mock fetch for audit logs query
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          items: [],
          total: 0,
          page: 1,
          pageSize: 20,
        },
      }),
    } as Response);

    renderWithClient(<AuditLogPage />);

    expect(screen.getByRole("heading", { name: "Audit Logs" })).toBeInTheDocument();
    expect(await screen.findByText("No audit records found")).toBeInTheDocument();

    // Open clear modal
    const clearBtn = screen.getByTitle("Clear Logs");
    await user.click(clearBtn);

    expect(screen.getByText("Clear All Audit Logs")).toBeInTheDocument();
    expect(screen.getByText(/Are you sure you want to clear all audit logs/)).toBeInTheDocument();

    // Cancel modal
    const cancelBtn = screen.getByRole("button", { name: "Cancel" });
    await user.click(cancelBtn);

    // Open clear modal again and dismiss with Escape
    await user.click(clearBtn);
    expect(screen.getByText("Clear All Audit Logs")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByText("Clear All Audit Logs")).not.toBeInTheDocument();
  });

  it("renders error state when fetch fails and allows retry", async () => {
    const user = userEvent.setup();

    const fetchSpy = vi.spyOn(globalThis, "fetch")
      .mockRejectedValueOnce(new Error("Server offline"))
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          code: "OK",
          message: "success",
          data: {
            items: [],
            total: 0,
            page: 1,
            pageSize: 20,
          },
        }),
      } as Response);

    renderWithClient(<AuditLogPage />);

    expect(await screen.findByText("Server offline")).toBeInTheDocument();
    const retryBtn = screen.getByRole("button", { name: "Retry" });
    await user.click(retryBtn);

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(await screen.findByText("No audit records found")).toBeInTheDocument();
  });
});
