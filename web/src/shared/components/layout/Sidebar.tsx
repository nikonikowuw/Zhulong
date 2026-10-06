import { Activity, Camera, ChevronLeft, ChevronRight, Cpu, LayoutGrid, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { UserNav } from "@/features/auth";
import type { FC } from "react";

export type ActiveTab = "overview" | "live" | "cameras";

interface SidebarProps {
  activeTab: ActiveTab;
  onSwitchTab: (tab: ActiveTab) => void;
  isCollapsed: boolean;
  onToggleCollapse: () => void;
  isMobileOpen: boolean;
  onCloseMobile: () => void;
  onlineCameraCount?: number;
  totalCameraCount?: number;
}

export const Sidebar: FC<SidebarProps> = ({
  activeTab,
  onSwitchTab,
  isCollapsed,
  onToggleCollapse,
  isMobileOpen,
  onCloseMobile,
  onlineCameraCount,
  totalCameraCount,
}) => {
  const { t } = useTranslation();

  const navItems = [
    {
      id: "overview" as const,
      label: t("nav.overview"),
      icon: Activity,
      badge: null,
    },
    {
      id: "live" as const,
      label: t("nav.live"),
      icon: LayoutGrid,
      badge: (
        <span
          className="flex h-2 w-2 rounded-full bg-[var(--positive)] animate-pulse"
          title="Live Ready"
          aria-hidden="true"
        />
      ),
    },
    {
      id: "cameras" as const,
      label: t("nav.cameras"),
      icon: Camera,
      badge:
        totalCameraCount !== undefined && totalCameraCount > 0 ? (
          <span className="rounded-full bg-[var(--surface-muted)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--muted)] border border-[var(--border)]">
            {onlineCameraCount ?? 0}/{totalCameraCount}
          </span>
        ) : null,
    },
  ];

  return (
    <>
      {/* Mobile Backdrop */}
      {isMobileOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 backdrop-blur-xs md:hidden"
          onClick={onCloseMobile}
          aria-hidden="true"
        />
      )}

      {/* Sidebar Container */}
      <aside
        className={`fixed top-0 bottom-0 left-0 z-50 flex flex-col border-r border-[var(--border)] bg-[var(--surface)] text-[var(--foreground)] transition-all duration-200 ease-in-out md:static ${
          isCollapsed ? "w-16" : "w-60"
        } ${isMobileOpen ? "translate-x-0" : "-translate-x-full md:translate-x-0"}`}
        aria-label="Sidebar Navigation"
      >
        {/* Brand Header */}
        <div className="flex h-14 shrink-0 items-center justify-between border-b border-[var(--border)] px-3">
          <a
            href="/"
            className="flex items-center gap-2.5 overflow-hidden font-semibold tracking-tight text-[var(--foreground)] hover:opacity-90 transition-opacity"
            aria-label={t("app.brand")}
          >
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[var(--accent)] text-white shadow-xs">
              <Cpu size={18} aria-hidden="true" />
            </div>
            {!isCollapsed && (
              <div className="flex flex-col min-w-0">
                <div className="flex items-center gap-1.5 leading-none">
                  <span className="text-sm font-bold tracking-tight">{t("app.brand")}</span>
                  <span className="rounded bg-[var(--accent)]/10 px-1 py-0.5 text-[9px] font-semibold text-[var(--accent)] leading-none uppercase">
                    {t("console.tag")}
                  </span>
                </div>
                <span className="text-[10px] text-[var(--muted)] truncate mt-1">
                  {t("app.environment")}
                </span>
              </div>
            )}
          </a>

          {/* Desktop Collapse Button */}
          {!isCollapsed && (
            <button
              type="button"
              className="hidden md:flex h-7 w-7 items-center justify-center rounded-md text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
              onClick={onToggleCollapse}
              aria-label={t("console.collapse")}
              title={t("console.collapse")}
            >
              <ChevronLeft size={16} aria-hidden="true" />
            </button>
          )}
        </div>

        {/* Navigation Menu */}
        <nav className="flex-1 overflow-y-auto px-2 py-3 space-y-1" aria-label="Module Navigation">
          <div className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--muted)]">
            {!isCollapsed && <span>{t("nav.overview")}</span>}
          </div>
          {navItems.map((item) => {
            const Icon = item.icon;
            const isActive = activeTab === item.id;
            return (
              <button
                key={item.id}
                type="button"
                className={`group flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-xs font-medium transition-all ${
                  isActive
                    ? "bg-[var(--accent)] text-white shadow-xs font-semibold"
                    : "text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)]"
                } ${isCollapsed ? "justify-center px-0" : ""}`}
                onClick={() => {
                  onSwitchTab(item.id);
                  onCloseMobile();
                }}
                title={isCollapsed ? item.label : undefined}
                aria-current={isActive ? "page" : undefined}
              >
                <Icon
                  size={17}
                  className={`shrink-0 transition-transform group-hover:scale-105 ${
                    isActive ? "text-white" : ""
                  }`}
                  aria-hidden="true"
                />
                {!isCollapsed && (
                  <span className="flex-1 text-left truncate">{item.label}</span>
                )}
                {!isCollapsed && item.badge}
              </button>
            );
          })}
        </nav>

        {/* Sidebar Footer */}
        <div className="border-t border-[var(--border)] p-2 space-y-2">
          {/* User Profile & Logout */}
          <UserNav isCollapsed={isCollapsed} />

          {/* Quick Engine Status indicator */}
          {!isCollapsed && (
            <div className="flex items-center gap-2 rounded-lg bg-[var(--surface-muted)] px-2.5 py-1.5 text-[11px] text-[var(--muted)]">
              <ShieldCheck size={14} className="text-[var(--positive)] shrink-0" aria-hidden="true" />
              <div className="flex flex-col min-w-0">
                <span className="font-medium text-[var(--foreground)] leading-tight truncate">
                  {t("console.dashboard.systemCard")}
                </span>
                <span className="text-[10px] text-[var(--muted)] truncate">
                  v0.1.0 · {t("app.environment")}
                </span>
              </div>
            </div>
          )}

          {/* Expand Button when collapsed */}
          {isCollapsed && (
            <button
              type="button"
              className="flex w-full items-center justify-center rounded-lg py-2 text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
              onClick={onToggleCollapse}
              aria-label={t("console.expand")}
              title={t("console.expand")}
            >
              <ChevronRight size={16} aria-hidden="true" />
            </button>
          )}
        </div>
      </aside>
    </>
  );
};
