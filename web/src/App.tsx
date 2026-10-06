import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Activity, Camera, Cpu, Globe, Moon, Sun } from "lucide-react";
import { AuthGuard, AuthProvider, UserNav, useAuth } from "@/features/auth";
import { CameraPage } from "@/features/camera";
import { HealthPanel } from "@/features/systemStatus";
import { currentLanguage } from "@/shared/i18n";
import { useTheme } from "@/shared/theme/useTheme";

type ActiveTab = "overview" | "cameras";

function getTabFromHash(): ActiveTab {
  if (typeof window !== "undefined" && window.location.hash === "#cameras") {
    return "cameras";
  }
  return "overview";
}

function SystemStatusContent() {
  const { t } = useTranslation();
  return (
    <>
      <section className="page-heading" aria-labelledby="page-title">
        <h1 id="page-title">{t("overview.heading")}</h1>
        <p className="page-description">{t("overview.description")}</p>
      </section>
      <HealthPanel />
    </>
  );
}

function SystemStatusPage() {
  const { t, i18n } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const { phase } = useAuth();
  const [activeTab, setActiveTab] = useState<ActiveTab>(getTabFromHash);
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);
  const themeLabel = theme === "dark" ? t("controls.themeToLight") : t("controls.themeToDark");
  const ThemeIcon = theme === "dark" ? Sun : Moon;
  const isAuthenticated = phase === "authenticated";

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
      window.location.hash = tab === "cameras" ? "#cameras" : "#overview";
    }
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
    <div className="application flex min-h-dvh flex-col bg-background text-foreground">
      <div className="ambient-background" aria-hidden="true">
        <div className="ambient-orb ambient-orb-1" />
        <div className="ambient-orb ambient-orb-2" />
        <div className="ambient-orb ambient-orb-3" />
        <div className="ambient-orb ambient-orb-4" />
      </div>
      <a className="skip-link" href="#main">{t("a11y.skipToContent")}</a>
      <header className="topbar">
        <a className="brand" href="/" aria-label={t("app.brand")}>
          <span className="brand-mark">
            <Cpu size={16} aria-hidden="true" />
          </span>
          <span className="brand-name">{t("app.brand")}</span>
        </a>

        {isAuthenticated && (
          <nav className="header-nav flex items-center gap-1 bg-[var(--surface-muted)] p-1 rounded-xl border border-[var(--border)]" aria-label="Main Navigation">
            <button
              type="button"
              className={`flex items-center gap-1.5 px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                activeTab === "overview"
                  ? "bg-[var(--surface)] text-[var(--foreground)] shadow-xs"
                  : "text-[var(--muted)] hover:text-[var(--foreground)]"
              }`}
              onClick={() => switchTab("overview")}
            >
              <Activity size={13} aria-hidden="true" />
              <span>{t("nav.overview")}</span>
            </button>
            <button
              type="button"
              className={`flex items-center gap-1.5 px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                activeTab === "cameras"
                  ? "bg-[var(--surface)] text-[var(--foreground)] shadow-xs"
                  : "text-[var(--muted)] hover:text-[var(--foreground)]"
              }`}
              onClick={() => switchTab("cameras")}
            >
              <Camera size={13} aria-hidden="true" />
              <span>{t("nav.cameras")}</span>
            </button>
          </nav>
        )}

        <div className="header-controls">
          <UserNav />
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
              onChange={(event) => void i18n.changeLanguage(event.target.value)}
              aria-label={t("controls.language")}
            >
              <option value="en">English</option>
              <option value="zh-Hans">简体中文</option>
              <option value="zh-Hant">繁體中文</option>
            </select>
          </div>
          <button
            className="icon-button theme-button"
            type="button"
            onClick={toggleTheme}
            aria-label={themeLabel}
            title={themeLabel}
          >
            <ThemeIcon size={15} aria-hidden="true" />
          </button>
        </div>
      </header>

      {isAuthenticated ? (
        <main id="main" className="workspace flex-1" tabIndex={-1}>
          <AuthGuard>
            {activeTab === "cameras" ? <CameraPage /> : <SystemStatusContent />}
          </AuthGuard>
          <footer className="workspace-footer">
            <span>{t("footer.runtime")}</span>
            <span>{t("footer.hardware")}</span>
          </footer>
        </main>
      ) : (
        <main id="main" className="auth-canvas flex flex-1 flex-col items-center justify-center p-4" tabIndex={-1}>
          <AuthGuard>
            <SystemStatusContent />
          </AuthGuard>
          <footer className="mt-6 flex items-center gap-2 text-xs text-[var(--muted)] opacity-70">
            <span>{t("app.brand")}</span>
            <span>·</span>
            <span>{t("footer.runtime")}</span>
          </footer>
        </main>
      )}
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
