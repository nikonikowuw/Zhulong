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
        className="flex min-h-[60vh] flex-col items-center justify-center p-8 text-center"
        role="status"
      >
        <LoaderCircle
          className="animate-spin text-[var(--accent)]"
          size={32}
          aria-hidden="true"
        />
        <p className="mt-4 text-sm font-medium text-[var(--muted)]">
          {t("auth.loading")}
        </p>
      </div>
    );
  }

  if (phase === "error") {
    return (
      <div
        className="flex min-h-[60vh] flex-col items-center justify-center p-8 text-center"
        role="alert"
      >
        <AlertCircle
          className="text-[var(--danger)]"
          size={36}
          aria-hidden="true"
        />
        <h2 className="mt-4 text-xl font-bold text-[var(--foreground)]">
          {t("auth.loadError")}
        </h2>
        {error && (
          <p className="mt-2 text-sm text-[var(--muted)]">{error}</p>
        )}
        <button
          type="button"
          onClick={() => void checkAuth()}
          className="mt-6 inline-flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--foreground)] transition hover:bg-[var(--surface-hover)]"
        >
          <RotateCw size={16} aria-hidden="true" />
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
