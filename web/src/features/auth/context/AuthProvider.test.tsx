import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "./AuthProvider";
import { useAuth } from "../hooks/useAuth";

function TestConsumer() {
  const { phase, user, error, login, logout } = useAuth();
  return (
    <div>
      <span data-testid="phase">{phase}</span>
      <span data-testid="user">{user?.username ?? "none"}</span>
      {error && <span data-testid="error">{error}</span>}
      <button type="button" onClick={() => void login({ username: "admin", password: "pwd" })}>
        TestLogin
      </button>
      <button type="button" onClick={() => void logout()}>
        TestLogout
      </button>
    </div>
  );
}

function renderWithProviders() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <TestConsumer />
      </AuthProvider>
    </QueryClientProvider>,
  );
}

describe("AuthProvider", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("sets uninitialized phase when system status initialized is false", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string | Request) => {
      const u = typeof url === "string" ? url : url.url;
      if (u.includes("/api/v1/auth/status")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { initialized: false } }),
        });
      }
      return Promise.reject(new Error("unexpected url"));
    }));

    renderWithProviders();
    await waitFor(() => {
      expect(screen.getByTestId("phase")).toHaveTextContent("uninitialized");
    });
  });

  it("sets authenticated phase when initialized and me succeeds", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string | Request) => {
      const u = typeof url === "string" ? url : url.url;
      if (u.includes("/api/v1/auth/status")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { initialized: true } }),
        });
      }
      if (u.includes("/api/v1/auth/me")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { id: 1, username: "superadmin" } }),
        });
      }
      return Promise.reject(new Error("unexpected url"));
    }));

    renderWithProviders();
    await waitFor(() => {
      expect(screen.getByTestId("phase")).toHaveTextContent("authenticated");
      expect(screen.getByTestId("user")).toHaveTextContent("superadmin");
    });
  });

  it("sets error phase when status check fails with network error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("Network failed")));

    renderWithProviders();
    await waitFor(() => {
      expect(screen.getByTestId("phase")).toHaveTextContent("error");
    });
  });

  it("allows login and sets phase to authenticated", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string | Request) => {
      const u = typeof url === "string" ? url : url.url;
      if (u.includes("/api/v1/auth/status")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { initialized: true } }),
        });
      }
      if (u.includes("/api/v1/auth/me")) {
        return Promise.resolve({
          ok: false,
          status: 401,
          json: async () => ({ code: "UNAUTHORIZED", message: "Unauthorized" }),
        });
      }
      if (u.includes("/api/v1/auth/login")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => ({ code: "OK", message: "Success", data: { id: 2, username: "admin" } }),
        });
      }
      return Promise.reject(new Error("unexpected url"));
    }));

    renderWithProviders();
    await waitFor(() => {
      expect(screen.getByTestId("phase")).toHaveTextContent("unauthenticated");
    });

    await user.click(screen.getByRole("button", { name: "TestLogin" }));
    await waitFor(() => {
      expect(screen.getByTestId("phase")).toHaveTextContent("authenticated");
      expect(screen.getByTestId("user")).toHaveTextContent("admin");
    });
  });
});
