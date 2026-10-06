import i18n from "@/shared/i18n";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Sidebar } from "./Sidebar";

describe("Sidebar", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  afterEach(() => {
    cleanup();
  });

  it("renders navigation items and responds to tab switching", async () => {
    const user = userEvent.setup();
    const onSwitchTab = vi.fn();
    const onToggleCollapse = vi.fn();
    const onCloseMobile = vi.fn();

    render(
      <Sidebar
        activeTab="overview"
        onSwitchTab={onSwitchTab}
        isCollapsed={false}
        onToggleCollapse={onToggleCollapse}
        isMobileOpen={false}
        onCloseMobile={onCloseMobile}
        onlineCameraCount={2}
        totalCameraCount={3}
      />,
    );

    expect(screen.getByRole("button", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Live" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cameras 2/3" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Live" }));
    expect(onSwitchTab).toHaveBeenCalledWith("live");

    const collapseBtn = screen.getByRole("button", { name: "Collapse sidebar" });
    await user.click(collapseBtn);
    expect(onToggleCollapse).toHaveBeenCalled();
  });

  it("renders expand button when collapsed", async () => {
    const user = userEvent.setup();
    const onToggleCollapse = vi.fn();

    render(
      <Sidebar
        activeTab="overview"
        onSwitchTab={vi.fn()}
        isCollapsed={true}
        onToggleCollapse={onToggleCollapse}
        isMobileOpen={false}
        onCloseMobile={vi.fn()}
      />,
    );

    const expandBtn = screen.getByRole("button", { name: "Expand sidebar" });
    expect(expandBtn).toBeInTheDocument();
    await user.click(expandBtn);
    expect(onToggleCollapse).toHaveBeenCalled();
  });
});
