import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Lock, LogIn, User as UserIcon } from "lucide-react";
import { useAuth } from "../hooks/useAuth";
import { AuthCard } from "./AuthCard";

export function LoginForm() {
  const { t } = useTranslation();
  const { login, error, clearError } = useAuth();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) return;

    setLocalError(null);
    clearError();
    setIsSubmitting(true);
    try {
      await login({ username: username.trim(), password });
    } catch (err) {
      if (err instanceof Error) {
        setLocalError(err.message);
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  const activeError = localError || error;

  return (
    <AuthCard
      icon={<Lock size={24} aria-hidden="true" />}
      title={t("auth.loginTitle")}
      subtitle={t("auth.loginSubtitle")}
      error={activeError}
    >
      <form onSubmit={handleSubmit} className="space-y-4">
        <div>
          <label
            htmlFor="login-username"
            className="mb-1 block text-sm font-medium text-[var(--foreground)]"
          >
            {t("auth.username")}
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[var(--muted)]">
              <UserIcon size={16} aria-hidden="true" />
            </span>
            <input
              id="login-username"
              name="username"
              type="text"
              autoComplete="username"
              required
              disabled={isSubmitting}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t("auth.usernamePlaceholder")}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] py-2.5 pr-3 pl-9 text-sm text-[var(--foreground)] outline-none transition focus:border-[var(--focus)] focus:ring-2 focus:ring-[var(--focus-shadow)]"
            />
          </div>
        </div>

        <div>
          <label
            htmlFor="login-password"
            className="mb-1 block text-sm font-medium text-[var(--foreground)]"
          >
            {t("auth.password")}
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[var(--muted)]">
              <Lock size={16} aria-hidden="true" />
            </span>
            <input
              id="login-password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              disabled={isSubmitting}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("auth.passwordPlaceholder")}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] py-2.5 pr-3 pl-9 text-sm text-[var(--foreground)] outline-none transition focus:border-[var(--focus)] focus:ring-2 focus:ring-[var(--focus-shadow)]"
            />
          </div>
        </div>

        <button
          type="submit"
          disabled={isSubmitting || !username.trim() || !password}
          className="mt-6 flex w-full items-center justify-center gap-2 rounded-lg bg-[var(--button-background)] py-2.5 text-sm font-semibold text-[var(--button-foreground)] transition hover:bg-[var(--button-hover)] disabled:opacity-50"
        >
          <LogIn size={16} aria-hidden="true" />
          {isSubmitting ? t("auth.submitting") : t("auth.loginButton")}
        </button>
      </form>
    </AuthCard>
  );
}
