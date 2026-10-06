import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConsoleTopbar } from "./ConsoleTopbar";

describe("ConsoleTopbar", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("renders breadcrumb and triggers actions without UserNav bloat", async () => {
    const user = userEvent.setup();
    const onOpenMobile = vi.fn();
    const onToggleTheme = vi.fn();
    const onChangeLang = vi.fn();
    const onRefresh = vi.fn();

    render(
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
      />,
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
