import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Cpu, Moon, Sun } from "lucide-react";
import { AuthGuard, AuthProvider, UserNav } from "@/features/auth";
import { HealthPanel } from "@/features/systemStatus";
import { currentLanguage } from "@/shared/i18n";
import { useTheme } from "@/shared/theme/useTheme";

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
  const language = currentLanguage(i18n.resolvedLanguage ?? i18n.language);
  const themeLabel = theme === "dark" ? t("controls.themeToLight") : t("controls.themeToDark");
  const ThemeIcon = theme === "dark" ? Sun : Moon;

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
    <div className="application min-h-dvh bg-background text-foreground">
      <a className="skip-link" href="#main">{t("a11y.skipToContent")}</a>
      <header className="topbar">
        <a className="brand" href="/" aria-label={t("app.brand")}>
          <span className="brand-mark"><Cpu size={19} aria-hidden="true" /></span>
          <span className="brand-name">{t("app.brand")}</span>
          <span className="brand-context">{t("app.environment")}</span>
        </a>
        <div className="header-controls">
          <UserNav />
          <label className="language-control">
            <span>{t("controls.language")}</span>
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
          </label>
          <button
            className="icon-button theme-button"
            type="button"
            onClick={toggleTheme}
            aria-label={themeLabel}
            title={themeLabel}
          >
            <ThemeIcon size={18} aria-hidden="true" />
          </button>
        </div>
      </header>

      <main id="main" className="workspace" tabIndex={-1}>
        <AuthGuard>
          <SystemStatusContent />
        </AuthGuard>
        <footer className="workspace-footer">
          <span>{t("footer.runtime")}</span>
          <span>{t("footer.hardware")}</span>
        </footer>
      </main>
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
