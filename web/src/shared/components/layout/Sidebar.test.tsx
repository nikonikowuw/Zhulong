import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthContext, type AuthContextValue } from "@/features/auth/context/authContext";
import { Sidebar } from "./Sidebar";

function renderSidebarWithAuth(sidebarProps: Parameters<typeof Sidebar>[0], authOverrides?: Partial<AuthContextValue>) {
  const defaultAuth: AuthContextValue = {
    phase: "authenticated",
    user: { id: 1, username: "admin" },
    error: null,
    clearError: vi.fn(),
    initAdmin: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    checkAuth: vi.fn(),
    ...authOverrides,
  };

  return {
    ...render(
      <AuthContext.Provider value={defaultAuth}>
        <Sidebar {...sidebarProps} />
      </AuthContext.Provider>,
    ),
    auth: defaultAuth,
  };
}

describe("Sidebar", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("renders navigation items, user info and responds to tab switching", async () => {
    const user = userEvent.setup();
    const onSwitchTab = vi.fn();
    const onToggleCollapse = vi.fn();
    const onCloseMobile = vi.fn();

    const { auth } = renderSidebarWithAuth({
      activeTab: "overview",
      onSwitchTab,
      isCollapsed: false,
      onToggleCollapse,
      isMobileOpen: false,
      onCloseMobile,
      onlineCameraCount: 2,
      totalCameraCount: 3,
    });

    expect(screen.getByRole("button", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Live" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cameras 2/3" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Audit Logs" })).toBeInTheDocument();

    // User is displayed in sidebar footer
    expect(screen.getByText("admin")).toBeInTheDocument();
    const logoutBtn = screen.getByRole("button", { name: "Logout" });
    expect(logoutBtn).toBeInTheDocument();
    await user.click(logoutBtn);
    expect(auth.logout).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Live" }));
    expect(onSwitchTab).toHaveBeenCalledWith("live");

    await user.click(screen.getByRole("button", { name: "Audit Logs" }));
    expect(onSwitchTab).toHaveBeenCalledWith("audit");

    const collapseBtn = screen.getByRole("button", { name: "Collapse sidebar" });
    await user.click(collapseBtn);
    expect(onToggleCollapse).toHaveBeenCalled();
  });

  it("renders expand button and compact logout when collapsed", async () => {
    const user = userEvent.setup();
    const onToggleCollapse = vi.fn();

    const { auth } = renderSidebarWithAuth({
      activeTab: "overview",
      onSwitchTab: vi.fn(),
      isCollapsed: true,
      onToggleCollapse,
      isMobileOpen: false,
      onCloseMobile: vi.fn(),
    });

    // Compact logout button is present
    const compactLogoutBtn = screen.getByRole("button", { name: "Logout" });
    expect(compactLogoutBtn).toBeInTheDocument();
    expect(compactLogoutBtn).toHaveAttribute("title", "admin (Logout)");
    await user.click(compactLogoutBtn);
    expect(auth.logout).toHaveBeenCalledTimes(1);

    const expandBtn = screen.getByRole("button", { name: "Expand sidebar" });
    expect(expandBtn).toBeInTheDocument();
    await user.click(expandBtn);
    expect(onToggleCollapse).toHaveBeenCalled();
  });
});
