import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { InitForm } from "./InitForm";
import { AuthContext, type AuthContextValue } from "../context/authContext";

function renderInitForm(authOverrides?: Partial<AuthContextValue>) {
  const defaultAuth: AuthContextValue = {
    phase: "uninitialized",
    user: null,
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
        <InitForm />
      </AuthContext.Provider>,
    ),
    auth: defaultAuth,
  };
}

describe("InitForm", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("renders setup fields", () => {
    renderInitForm();
    expect(screen.getByRole("heading", { name: "System Setup" })).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.getByLabelText("Confirm Password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Complete Setup & Enter" })).toBeInTheDocument();
  });

  it("warns when password is shorter than 8 characters", async () => {
    const user = userEvent.setup();
    const initMock = vi.fn();
    renderInitForm({ initAdmin: initMock });

    await user.type(screen.getByLabelText("Password"), "short");
    await user.type(screen.getByLabelText("Confirm Password"), "short");
    await user.click(screen.getByRole("button", { name: "Complete Setup & Enter" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Password must be at least 8 characters");
    expect(initMock).not.toHaveBeenCalled();
  });

  it("warns when passwords do not match", async () => {
    const user = userEvent.setup();
    const initMock = vi.fn();
    renderInitForm({ initAdmin: initMock });

    await user.type(screen.getByLabelText("Password"), "password123");
    await user.type(screen.getByLabelText("Confirm Password"), "password456");
    await user.click(screen.getByRole("button", { name: "Complete Setup & Enter" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Passwords do not match");
    expect(initMock).not.toHaveBeenCalled();
  });

  it("submits valid credentials successfully", async () => {
    const user = userEvent.setup();
    const initMock = vi.fn().mockResolvedValue(undefined);
    renderInitForm({ initAdmin: initMock });

    await user.type(screen.getByLabelText("Password"), "password123");
    await user.type(screen.getByLabelText("Confirm Password"), "password123");
    await user.click(screen.getByRole("button", { name: "Complete Setup & Enter" }));

    expect(initMock).toHaveBeenCalledWith({
      username: "admin",
      password: "password123",
      confirmPassword: "password123",
    });
  });

  it("displays password strength indicator when typing", async () => {
    const user = userEvent.setup();
    renderInitForm();

    const passwordInput = screen.getByLabelText("Password");
    await user.type(passwordInput, "pass123");
    expect(screen.getByText("Weak")).toBeInTheDocument();

    await user.type(passwordInput, "4");
    expect(screen.getByText("Medium")).toBeInTheDocument();

    await user.type(passwordInput, "!@#");
    expect(screen.getByText("Strong")).toBeInTheDocument();
  });
});
