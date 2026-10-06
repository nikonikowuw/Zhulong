import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthGuard, AuthProvider, useAuth } from "@/features/auth";
import { CameraPage, useCamerasQuery } from "@/features/camera";
import { LivePage } from "@/features/live";
import { OverviewDashboard } from "@/features/systemStatus";
import { ConsoleTopbar, Sidebar } from "@/shared/components/layout";
import type { ActiveTab } from "@/shared/components/layout";
import { currentLanguage } from "@/shared/i18n";
import { useTheme } from "@/shared/theme/useTheme";

const SIDEBAR_COLLAPSED_KEY = "zhulong.sidebar.collapsed.v1";

function getTabFromHash(): ActiveTab {
  if (typeof window !== "undefined") {
    if (window.location.hash === "#live") return "live";
    if (window.location.hash === "#cameras") return "cameras";
  }
  return "overview";
}

function readSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "true";
  } catch {
    return false;
  }
}

function SystemStatusPage() {
  const { t, i18n } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const { phase } = useAuth();
  const [activeTab, setActiveTab] = useState<ActiveTab>(getTabFromHash);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState<boolean>(readSidebarCollapsed);
  const [isMobileSidebarOpen, setIsMobileSidebarOpen] = useState(false);

  const isAuthenticated = phase === "authenticated";

  // Only query cameras when authenticated or on cameras/overview tab
  const { data: cameras, isFetching: isFetchingCameras, refetch: refetchCameras } = useCamerasQuery();
  const totalCameras = cameras?.length ?? 0;
  const onlineCameras = cameras?.filter((c) => c.enabled && c.health === "online").length ?? 0;

  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);
  const themeLabel = theme === "dark" ? t("controls.themeToLight") : t("controls.themeToDark");

  useEffect(() => {
    function handleHashChange() {
      setActiveTab(getTabFromHash());
    }
    window.addEventListener("hashchange", handleHashChange);
    return () => window.removeEventListener("hashchange", handleHashChange);
  }, []);

  function switchTab(tab: ActiveTab) {
    setActiveTab(tab);
    if (typeof window !== "undefined") {
      if (tab === "live") window.location.hash = "#live";
      else if (tab === "cameras") window.location.hash = "#cameras";
      else window.location.hash = "#overview";
    }
  }

  function toggleSidebarCollapse() {
    setIsSidebarCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(next));
      } catch {
        // Ignored
      }
      return next;
    });
  }

  useEffect(() => {
    document.documentElement.lang = language;
    document.title = `${t("app.title")} · ${t("app.brand")}`;
    document.querySelector('meta[name="description"]')?.setAttribute("content", t("app.description"));
  }, [language, t]);

  useEffect(() => {
    document.querySelector('meta[name="theme-color"]')?.setAttribute(
      "content",
      theme === "dark" ? "#1c1c1e" : "#f5f5f7",
    );
  }, [theme]);

  return (
    <div className="application flex min-h-dvh flex-col bg-[var(--background)] text-[var(--foreground)]">
      <div className="console-bg" aria-hidden="true" />
      <a className="skip-link" href="#main">{t("a11y.skipToContent")}</a>

      <div className="flex min-h-dvh w-full overflow-hidden">
        {/* Professional Management Sidebar */}
        {isAuthenticated && (
          <Sidebar
            activeTab={activeTab}
            onSwitchTab={switchTab}
            isCollapsed={isSidebarCollapsed}
            onToggleCollapse={toggleSidebarCollapse}
            isMobileOpen={isMobileSidebarOpen}
            onCloseMobile={() => setIsMobileSidebarOpen(false)}
            onlineCameraCount={onlineCameras}
            totalCameraCount={totalCameras}
          />
        )}

        {/* Main Content Area with Console Topbar */}
        <div className="flex flex-1 flex-col min-w-0 overflow-hidden">
          <ConsoleTopbar
            activeTab={activeTab}
            onOpenMobileSidebar={() => setIsMobileSidebarOpen(true)}
            theme={theme}
            onToggleTheme={toggleTheme}
            themeLabel={themeLabel}
            language={language}
            onChangeLanguage={(lang) => void i18n.changeLanguage(lang)}
            onlineCameraCount={isAuthenticated ? onlineCameras : undefined}
            totalCameraCount={isAuthenticated ? totalCameras : undefined}
            onRefresh={isAuthenticated ? () => void refetchCameras() : undefined}
            isRefreshing={isFetchingCameras}
          />

          {isAuthenticated ? (
            <main id="main" className="workspace flex-1 flex flex-col min-w-0 overflow-y-auto" tabIndex={-1}>
              <AuthGuard>
                {activeTab === "cameras" ? (
                  <CameraPage />
                ) : activeTab === "live" ? (
                  <LivePage />
                ) : (
                  <OverviewDashboard onNavigateTab={switchTab} />
                )}
              </AuthGuard>
              <footer className="workspace-footer mt-auto pt-6 border-t border-[var(--border)]/40 flex justify-between text-xs text-[var(--muted)]">
                <span>{t("footer.runtime")}</span>
                <span>{t("footer.hardware")}</span>
              </footer>
            </main>
          ) : (
            <main id="main" className="auth-canvas flex flex-1 flex-col items-center justify-center p-4" tabIndex={-1}>
              <AuthGuard>
                <OverviewDashboard onNavigateTab={switchTab} />
              </AuthGuard>
              <footer className="mt-6 flex items-center gap-2 text-xs text-[var(--muted)] opacity-70">
                <span>{t("app.brand")}</span>
                <span>·</span>
                <span>{t("footer.runtime")}</span>
              </footer>
            </main>
          )}
        </div>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <AuthProvider>
      <SystemStatusPage />
    </AuthProvider>
  );
}
