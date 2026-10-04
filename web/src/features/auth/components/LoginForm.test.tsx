import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LoginForm } from "./LoginForm";
import { AuthContext, type AuthContextValue } from "../context/authContext";

function renderLoginForm(authOverrides?: Partial<AuthContextValue>) {
  const defaultAuth: AuthContextValue = {
    phase: "unauthenticated",
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
        <LoginForm />
      </AuthContext.Provider>,
    ),
    auth: defaultAuth,
  };
}

describe("LoginForm", () => {
  beforeEach(async () => {
    localStorage.clear();
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
    vi.clearAllMocks();
  });

  it("renders username, password, and login button", () => {
    renderLoginForm();
    expect(screen.getByRole("heading", { name: "Administrator Login" })).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Login" })).toBeInTheDocument();
  });

  it("submits the form with entered credentials", async () => {
    const user = userEvent.setup();
    const loginMock = vi.fn().mockResolvedValue(undefined);
    renderLoginForm({ login: loginMock });

    await user.type(screen.getByLabelText("Username"), "admin");
    const passwordInput = screen.getByLabelText("Password");
    await user.type(passwordInput, "secret123");
    await user.click(screen.getByRole("button", { name: "Login" }));

    expect(loginMock).toHaveBeenCalledWith({
      username: "admin",
      password: "secret123",
    });
  });

  it("displays validation error if username is empty on submit", async () => {
    const user = userEvent.setup();
    const loginMock = vi.fn();
    renderLoginForm({ login: loginMock });

    await user.click(screen.getByRole("button", { name: "Login" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Username is required");
    expect(loginMock).not.toHaveBeenCalled();
  });

  it("displays validation error if password is empty on submit", async () => {
    const user = userEvent.setup();
    const loginMock = vi.fn();
    renderLoginForm({ login: loginMock });

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.click(screen.getByRole("button", { name: "Login" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Password is required");
    expect(loginMock).not.toHaveBeenCalled();
  });

  it("displays validation error on blur if username is left empty", async () => {
    const user = userEvent.setup();
    renderLoginForm();

    const usernameInput = screen.getByLabelText("Username");
    await user.click(usernameInput);
    await user.click(document.body);

    expect(await screen.findByRole("alert")).toHaveTextContent("Username is required");
    expect(usernameInput).toHaveAttribute("aria-invalid", "true");

    await user.type(usernameInput, "admin");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("displays validation error on blur if password is left empty", async () => {
    const user = userEvent.setup();
    renderLoginForm();

    const passwordInput = screen.getByLabelText("Password");
    await user.click(passwordInput);
    await user.click(document.body);

    expect(await screen.findByRole("alert")).toHaveTextContent("Password is required");
    expect(passwordInput).toHaveAttribute("aria-invalid", "true");
  });

  it("displays error message if login fails", async () => {
    const user = userEvent.setup();
    const loginMock = vi.fn().mockRejectedValue(new Error("Invalid username or password"));
    renderLoginForm({ login: loginMock });

    await user.type(screen.getByLabelText("Username"), "admin");
    const passwordInput = screen.getByLabelText("Password");
    await user.type(passwordInput, "wrongpassword");
    await user.click(screen.getByRole("button", { name: "Login" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid username or password");
  });
});
