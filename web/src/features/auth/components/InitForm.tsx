import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle, KeyRound, Lock, User as UserIcon } from "lucide-react";
import { useAuth } from "../hooks/useAuth";
import { AuthCard } from "./AuthCard";

type StrengthLevel = "weak" | "medium" | "strong";

function getPasswordStrength(password: string): StrengthLevel {
  if (password.length < 8) return "weak";
  const hasLetters = /[a-zA-Z]/.test(password);
  const hasNumbers = /[0-9]/.test(password);
  const hasSymbols = /[^a-zA-Z0-9]/.test(password);

  if (password.length >= 10 && hasLetters && hasNumbers && hasSymbols) {
    return "strong";
  }
  if (hasLetters && hasNumbers) {
    return "medium";
  }
  return "weak";
}

const strengthColors: Record<StrengthLevel, string> = {
  weak: "text-[var(--danger)]",
  medium: "text-[var(--accent)]",
  strong: "text-[var(--positive)]",
};

export function InitForm() {
  const { t } = useTranslation();
  const { initAdmin, error, clearError } = useAuth();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    clearError();

    if (password.length < 8) {
      setLocalError(t("auth.passwordTooShort"));
      return;
    }
    if (password !== confirmPassword) {
      setLocalError(t("auth.passwordMismatch"));
      return;
    }

    setIsSubmitting(true);
    try {
      await initAdmin({
        username: username.trim(),
        password,
        confirmPassword,
      });
    } catch (err) {
      if (err instanceof Error) {
        setLocalError(err.message);
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  const activeError = localError || error;
  const strength = password ? getPasswordStrength(password) : null;
  const strengthLabel = strength
    ? t(`auth.strength${strength.charAt(0).toUpperCase() + strength.slice(1)}`)
    : "";

  return (
    <AuthCard
      icon={<KeyRound size={24} aria-hidden="true" />}
      title={t("auth.setupTitle")}
      subtitle={t("auth.setupSubtitle")}
      error={activeError}
    >
      <form onSubmit={handleSubmit} className="space-y-4">
        <div>
          <label
            htmlFor="init-username"
            className="mb-1 block text-sm font-medium text-[var(--foreground)]"
          >
            {t("auth.username")}
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[var(--muted)]">
              <UserIcon size={16} aria-hidden="true" />
            </span>
            <input
              id="init-username"
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
            htmlFor="init-password"
            className="mb-1 block text-sm font-medium text-[var(--foreground)]"
          >
            {t("auth.password")}
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[var(--muted)]">
              <Lock size={16} aria-hidden="true" />
            </span>
            <input
              id="init-password"
              name="password"
              type="password"
              autoComplete="new-password"
              required
              disabled={isSubmitting}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("auth.passwordPlaceholder")}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] py-2.5 pr-3 pl-9 text-sm text-[var(--foreground)] outline-none transition focus:border-[var(--focus)] focus:ring-2 focus:ring-[var(--focus-shadow)]"
            />
          </div>
          {strength && (
            <div className="mt-1.5 flex items-center justify-between text-xs">
              <span className="text-[var(--muted)]">
                {t("auth.passwordStrength", { strength: strengthLabel })}
              </span>
              <span className={`font-semibold ${strengthColors[strength]}`}>
                {strengthLabel}
              </span>
            </div>
          )}
        </div>

        <div>
          <label
            htmlFor="init-confirm-password"
            className="mb-1 block text-sm font-medium text-[var(--foreground)]"
          >
            {t("auth.confirmPassword")}
          </label>
          <div className="relative">
            <span className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-[var(--muted)]">
              <Lock size={16} aria-hidden="true" />
            </span>
            <input
              id="init-confirm-password"
              name="confirmPassword"
              type="password"
              autoComplete="new-password"
              required
              disabled={isSubmitting}
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder={t("auth.confirmPasswordPlaceholder")}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] py-2.5 pr-3 pl-9 text-sm text-[var(--foreground)] outline-none transition focus:border-[var(--focus)] focus:ring-2 focus:ring-[var(--focus-shadow)]"
            />
          </div>
        </div>

        <button
          type="submit"
          disabled={isSubmitting || !username.trim() || !password || !confirmPassword}
          className="mt-6 flex w-full items-center justify-center gap-2 rounded-lg bg-[var(--button-background)] py-2.5 text-sm font-semibold text-[var(--button-foreground)] transition hover:bg-[var(--button-hover)] disabled:opacity-50"
        >
          <CheckCircle size={16} aria-hidden="true" />
          {isSubmitting ? t("auth.submitting") : t("auth.initButton")}
        </button>
      </form>
    </AuthCard>
  );
}
