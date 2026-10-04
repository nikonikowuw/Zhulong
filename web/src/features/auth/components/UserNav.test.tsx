import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { UserNav } from "./UserNav";
import { AuthContext, type AuthContextValue } from "../context/authContext";

function renderUserNav(authOverrides?: Partial<AuthContextValue>) {
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
        <UserNav />
      </AuthContext.Provider>,
    ),
    auth: defaultAuth,
  };
}

describe("UserNav", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("renders nothing when unauthenticated", () => {
    renderUserNav({ phase: "unauthenticated", user: null });
    expect(screen.queryByRole("button", { name: "Logout" })).not.toBeInTheDocument();
  });

  it("renders username and logout button when authenticated", () => {
    renderUserNav();
    expect(screen.getByText("admin")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Logout" })).toBeInTheDocument();
  });

  it("triggers logout when clicked", async () => {
    const user = userEvent.setup();
    const logoutMock = vi.fn().mockResolvedValue(undefined);
    renderUserNav({ logout: logoutMock });

    await user.click(screen.getByRole("button", { name: "Logout" }));
    expect(logoutMock).toHaveBeenCalledTimes(1);
  });
});
