import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/features/auth";
import { ConsoleTopbar } from "./ConsoleTopbar";

describe("ConsoleTopbar", () => {
  let queryClient: QueryClient;

  beforeEach(async () => {
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Infinity } },
    });
    await i18n.changeLanguage("en");
    vi.stubGlobal("fetch", vi.fn().mockImplementation((input: string | Request) => {
      const url = typeof input === "string" ? input : input.url;
      if (url.includes("/api/v1/auth/status")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { initialized: true } }),
        });
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => ({ code: "OK", message: "Success", data: { id: 1, username: "admin" } }),
      });
    }));
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders breadcrumb and triggers actions", async () => {
    const user = userEvent.setup();
    const onOpenMobile = vi.fn();
    const onToggleTheme = vi.fn();
    const onChangeLang = vi.fn();
    const onRefresh = vi.fn();

    render(
      <QueryClientProvider client={queryClient}>
        <AuthProvider>
          <ConsoleTopbar
            activeTab="live"
            onOpenMobileSidebar={onOpenMobile}
            theme="light"
            onToggleTheme={onToggleTheme}
            themeLabel="Use dark theme"
            language="en"
            onChangeLanguage={onChangeLang}
            onlineCameraCount={3}
            totalCameraCount={5}
            onRefresh={onRefresh}
          />
        </AuthProvider>
      </QueryClientProvider>,
    );

    // Breadcrumb displays Live
    expect(screen.getByText("Live")).toBeInTheDocument();
    expect(screen.getByText("3/5 Online")).toBeInTheDocument();

    // Refresh action
    const refreshBtn = screen.getByRole("button", { name: "Refresh data" });
    await user.click(refreshBtn);
    expect(onRefresh).toHaveBeenCalledTimes(1);

    // Theme toggle
    const themeBtn = screen.getByRole("button", { name: "Use dark theme" });
    await user.click(themeBtn);
    expect(onToggleTheme).toHaveBeenCalledTimes(1);

    // Language change
    const langSelect = screen.getByRole("combobox", { name: "Language" });
    await user.selectOptions(langSelect, "zh-Hans");
    expect(onChangeLang).toHaveBeenCalledWith("zh-Hans");
  });
});
