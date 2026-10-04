import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, LoaderCircle, RotateCw } from "lucide-react";
import { useAuth } from "../hooks/useAuth";
import { InitForm } from "./InitForm";
import { LoginForm } from "./LoginForm";

export function AuthGuard({ children }: { children: ReactNode }) {
  const { phase, error, checkAuth } = useAuth();
  const { t } = useTranslation();

  if (phase === "loading") {
    return (
      <div
        className="flex min-h-[50vh] flex-col items-center justify-center p-8 text-center"
        role="status"
      >
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--surface-muted)] text-[var(--accent)] ring-1 ring-black/[0.04] dark:ring-white/[0.08] shadow-[0_2px_6px_rgba(0,0,0,0.03)]">
          <LoaderCircle
            className="animate-spin"
            size={22}
            aria-hidden="true"
          />
        </div>
        <p className="mt-3.5 text-xs font-medium tracking-tight text-[var(--muted)]">
          {t("auth.loading")}
        </p>
      </div>
    );
  }

  if (phase === "error") {
    return (
      <div
        className="flex min-h-[50vh] flex-col items-center justify-center p-8 text-center"
        role="alert"
      >
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--danger-soft)] text-[var(--danger)]">
          <AlertCircle size={22} aria-hidden="true" />
        </div>
        <h2 className="mt-4 text-base md:text-lg font-semibold tracking-tight text-[var(--foreground)]">
          {t("auth.loadError")}
        </h2>
        {error && (
          <p className="mt-1.5 max-w-[42ch] text-xs text-[var(--muted)] leading-relaxed">
            {error}
          </p>
        )}
        <button
          type="button"
          onClick={() => void checkAuth()}
          className="mt-5 inline-flex items-center gap-2 rounded-full bg-[var(--surface-muted)] px-5 py-2 text-xs font-medium text-[var(--foreground)] transition-all duration-200 hover:bg-[var(--surface-hover)] active:scale-95"
        >
          <RotateCw size={14} aria-hidden="true" />
          <span>{t("auth.retry")}</span>
        </button>
      </div>
    );
  }

  if (phase === "uninitialized") {
    return <InitForm />;
  }

  if (phase === "unauthenticated") {
    return <LoginForm />;
  }

  return <>{children}</>;
}
