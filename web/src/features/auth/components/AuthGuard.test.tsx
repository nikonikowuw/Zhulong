import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { AuthGuard } from "./AuthGuard";
import { AuthContext, type AuthContextValue } from "../context/authContext";

function renderGuard(phase: AuthContextValue["phase"]) {
  const authValue: AuthContextValue = {
    phase,
    user: phase === "authenticated" ? { id: 1, username: "admin" } : null,
    error: null,
    clearError: () => {},
    initAdmin: async () => {},
    login: async () => {},
    logout: async () => {},
    checkAuth: async () => {},
  };

  return render(
    <AuthContext.Provider value={authValue}>
      <AuthGuard>
        <div data-testid="protected-content">Protected Dashboard</div>
      </AuthGuard>
    </AuthContext.Provider>,
  );
}

describe("AuthGuard", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("renders loading spinner while checking auth", () => {
    renderGuard("loading");
    expect(screen.getByRole("status")).toHaveTextContent("Verifying session...");
    expect(screen.queryByTestId("protected-content")).not.toBeInTheDocument();
  });

  it("renders InitForm when system is uninitialized", () => {
    renderGuard("uninitialized");
    expect(screen.getByRole("heading", { name: "System Setup" })).toBeInTheDocument();
    expect(screen.queryByTestId("protected-content")).not.toBeInTheDocument();
  });

  it("renders LoginForm when user is unauthenticated", () => {
    renderGuard("unauthenticated");
    expect(screen.getByRole("heading", { name: "Administrator Login" })).toBeInTheDocument();
    expect(screen.queryByTestId("protected-content")).not.toBeInTheDocument();
  });

  it("renders error alert with retry button when phase is error", () => {
    renderGuard("error");
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByTestId("protected-content")).not.toBeInTheDocument();
  });

  it("renders protected content when authenticated", () => {
    renderGuard("authenticated");
    expect(screen.getByTestId("protected-content")).toBeInTheDocument();
  });
});
