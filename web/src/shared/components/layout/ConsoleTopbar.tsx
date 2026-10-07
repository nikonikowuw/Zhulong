import { Globe, Maximize2, Menu, Minimize2, Moon, RefreshCw, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { FC } from "react";
import type { ActiveTab } from "./Sidebar";

interface ConsoleTopbarProps {
  activeTab: ActiveTab;
  onOpenMobileSidebar: () => void;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  themeLabel: string;
  language: string;
  onChangeLanguage: (lang: string) => void;
  onlineCameraCount?: number;
  totalCameraCount?: number;
  onRefresh?: () => void;
  isRefreshing?: boolean;
}

export const ConsoleTopbar: FC<ConsoleTopbarProps> = ({
  activeTab,
  onOpenMobileSidebar,
  theme,
  onToggleTheme,
  themeLabel,
  language,
  onChangeLanguage,
  onlineCameraCount,
  totalCameraCount,
  onRefresh,
  isRefreshing = false,
}) => {
  const { t } = useTranslation();
  const [isFullscreen, setIsFullscreen] = useState(false);

  useEffect(() => {
    function handleFullscreenChange() {
      setIsFullscreen(Boolean(document.fullscreenElement));
    }
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
  }, []);

  const toggleFullscreen = () => {
    if (!document.fullscreenElement) {
      void document.documentElement.requestFullscreen().catch(() => {});
    } else {
      void document.exitFullscreen().catch(() => {});
    }
  };

  const getBreadcrumbTitle = () => {
    switch (activeTab) {
      case "live":
        return t("nav.live");
      case "cameras":
        return t("nav.cameras");
      case "audit":
        return t("nav.audit");
      default:
        return t("nav.overview");
    }
  };

  const ThemeIcon = theme === "dark" ? Sun : Moon;

  return (
    <header className="sticky top-0 z-30 flex h-14 w-full shrink-0 items-center justify-between border-b border-[var(--border)] bg-[var(--surface)] px-4 transition-colors">
      {/* Left section: Mobile menu + Breadcrumb & Status Pill */}
      <div className="flex items-center gap-3 min-w-0">
        <button
          type="button"
          className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] md:hidden transition-colors"
          onClick={onOpenMobileSidebar}
          aria-label="Open sidebar menu"
        >
          <Menu size={18} aria-hidden="true" />
        </button>

        {/* Breadcrumb */}
        <div className="flex items-center gap-2 text-xs font-medium text-[var(--muted)]">
          <span className="hidden sm:inline hover:text-[var(--foreground)] transition-colors">
            {t("app.brand")}
          </span>
          <span className="hidden sm:inline text-[var(--border)]">/</span>
          <span className="font-semibold text-[var(--foreground)] text-sm tracking-tight truncate">
            {getBreadcrumbTitle()}
          </span>
        </div>

        {/* Global Live Device Indicator */}
        {totalCameraCount !== undefined && totalCameraCount > 0 && (
          <div className="hidden lg:flex items-center gap-1.5 rounded-full bg-[var(--surface-muted)] px-2.5 py-1 text-[11px] font-medium text-[var(--muted)] border border-[var(--border)]">
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                (onlineCameraCount ?? 0) > 0 ? "bg-[var(--positive)] animate-pulse" : "bg-[var(--muted)]"
              }`}
              aria-hidden="true"
            />
            <span>
              {t("console.onlineStatus", {
                online: onlineCameraCount ?? 0,
                total: totalCameraCount,
              })}
            </span>
          </div>
        )}
      </div>

      {/* Right section: Global Actions & Controls */}
      <div className="flex items-center gap-2">
        {/* Global Refresh Button */}
        {onRefresh && (
          <button
            type="button"
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
            onClick={onRefresh}
            disabled={isRefreshing}
            aria-label={t("console.refresh")}
            title={t("console.refresh")}
          >
            <RefreshCw
              size={15}
              className={isRefreshing ? "animate-spin text-[var(--accent)]" : ""}
              aria-hidden="true"
            />
          </button>
        )}

        {/* Fullscreen Button */}
        <button
          type="button"
          className="hidden sm:flex h-8 w-8 items-center justify-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
          onClick={toggleFullscreen}
          aria-label={isFullscreen ? t("console.exitFullscreen") : t("console.fullscreen")}
          title={isFullscreen ? t("console.exitFullscreen") : t("console.fullscreen")}
        >
          {isFullscreen ? <Minimize2 size={15} aria-hidden="true" /> : <Maximize2 size={15} aria-hidden="true" />}
        </button>

        {/* Language Selector */}
        <div className="language-control relative flex items-center">
          <label htmlFor="language" className="sr-only">
            {t("controls.language")}
          </label>
          <span className="pointer-events-none absolute left-2.5 flex items-center text-[var(--muted)]">
            <Globe size={12} aria-hidden="true" />
          </span>
          <select
            id="language"
            name="language"
            value={language}
            onChange={(event) => onChangeLanguage(event.target.value)}
            aria-label={t("controls.language")}
            className="h-8 rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] pl-7 pr-3 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] focus:outline-hidden transition-colors"
          >
            <option value="en">English</option>
            <option value="zh-Hans">简体中文</option>
            <option value="zh-Hant">繁體中文</option>
          </select>
        </div>

        {/* Theme Toggle Button */}
        <button
          className="icon-button theme-button flex h-8 w-8 items-center justify-center rounded-lg text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
          type="button"
          onClick={onToggleTheme}
          aria-label={themeLabel}
          title={themeLabel}
        >
          <ThemeIcon size={15} aria-hidden="true" />
        </button>
      </div>
    </header>
  );
};
