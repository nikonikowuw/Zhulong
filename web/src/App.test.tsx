import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "@/App";
import { ThemeProvider } from "@/shared/theme/ThemeProvider";

const readyResponse = {
  code: "OK",
  message: "Success",
  data: {
    status: "ready",
    components: { database: "ready", engine: "ready" },
  },
};

function renderApp() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  return render(
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </ThemeProvider>,
  );
}

describe("App", () => {
  beforeEach(async () => {
    localStorage.clear();
    for (const [name, content] of Object.entries({
      description: "Runtime readiness for this Zhulong host.",
      "theme-color": "#f5f5f7",
    })) {
      let meta = document.head.querySelector<HTMLMetaElement>(`meta[name="${name}"]`);
      if (!meta) {
        meta = document.createElement("meta");
        meta.name = name;
        document.head.append(meta);
      }
      meta.content = content;
    }
    document.documentElement.lang = "en";
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
      if (url.includes("/api/v1/auth/me")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { id: 1, username: "admin" } }),
        });
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => readyResponse,
      });
    }));
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("updates the document language and persists a selected locale", async () => {
    const user = userEvent.setup();
    renderApp();

    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-Hans");

    expect(await screen.findByRole("heading", { name: "系统概览" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("zh-Hans");
    expect(document.title).toBe("系统状态 · Zhulong");
    expect(document.querySelector('meta[name="description"]')).toHaveAttribute("content", "Zhulong 主机的运行就绪状态。");
    expect(localStorage.getItem("zhulong.language.v1")).toBe("zh-Hans");
  });

  it("persists the selected visual theme", async () => {
    const user = userEvent.setup();
    renderApp();

    await user.click(screen.getByRole("button", { name: "Use dark theme" }));

    expect(screen.getByRole("button", { name: "Use light theme" })).toBeInTheDocument();
    expect(localStorage.getItem("zhulong.theme.v1")).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.querySelector('meta[name="theme-color"]')).toHaveAttribute("content", "#1c1c1e");
  });

  it("switches between Overview, Live, and Cameras tabs when authenticated", async () => {
    const user = userEvent.setup();
    renderApp();

    expect(await screen.findByRole("heading", { name: "System Overview" })).toBeInTheDocument();

    const liveNavBtn = screen.getByRole("button", { name: "Live" });
    await user.click(liveNavBtn);

    expect(await screen.findByRole("heading", { name: "Live Video Surveillance" })).toBeInTheDocument();
    expect(window.location.hash).toBe("#live");

    const camerasNavBtn = screen.getByRole("button", { name: "Cameras" });
    await user.click(camerasNavBtn);

    expect(await screen.findByRole("heading", { name: "Cameras" })).toBeInTheDocument();
    expect(window.location.hash).toBe("#cameras");

    const overviewNavBtn = screen.getByRole("button", { name: "Overview" });
    await user.click(overviewNavBtn);

    expect(await screen.findByRole("heading", { name: "System Overview" })).toBeInTheDocument();
    expect(window.location.hash).toBe("#overview");
  });
});
