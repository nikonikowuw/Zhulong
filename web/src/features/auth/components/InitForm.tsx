import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, CheckCircle, KeyRound, LoaderCircle, Lock, User as UserIcon } from "lucide-react";
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
  weak: "text-[#ff3b30]",
  medium: "text-[#0071e3]",
  strong: "text-[#34c759]",
};

export function InitForm() {
  const { t } = useTranslation();
  const { initAdmin, error, clearError } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [touched, setTouched] = useState({
    username: false,
    password: false,
    confirmPassword: false,
  });
  const [usernameError, setUsernameError] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [confirmPasswordError, setConfirmPasswordError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const validateUsername = (val: string): string | null => {
    if (!val.trim()) {
      return t("auth.usernameRequired");
    }
    return null;
  };

  const validatePassword = (val: string): string | null => {
    if (!val) {
      return t("auth.passwordRequired");
    }
    if (val.length < 8) {
      return t("auth.passwordTooShort");
    }
    return null;
  };

  const validateConfirmPassword = (confirmVal: string, pwdVal: string): string | null => {
    if (!confirmVal) {
      return t("auth.confirmPasswordRequired");
    }
    if (confirmVal !== pwdVal) {
      return t("auth.passwordMismatch");
    }
    return null;
  };

  const handleUsernameBlur = () => {
    setTouched((prev) => ({ ...prev, username: true }));
    setUsernameError(validateUsername(username));
  };

  const handlePasswordBlur = () => {
    setTouched((prev) => ({ ...prev, password: true }));
    setPasswordError(validatePassword(password));
    if (touched.confirmPassword && confirmPassword) {
      setConfirmPasswordError(validateConfirmPassword(confirmPassword, password));
    }
  };

  const handleConfirmPasswordBlur = () => {
    setTouched((prev) => ({ ...prev, confirmPassword: true }));
    setConfirmPasswordError(validateConfirmPassword(confirmPassword, password));
  };

  const handleUsernameChange = (val: string) => {
    setUsername(val);
    if (touched.username) {
      setUsernameError(validateUsername(val));
    }
    if (localError) setLocalError(null);
  };

  const handlePasswordChange = (val: string) => {
    setPassword(val);
    if (touched.password) {
      setPasswordError(validatePassword(val));
    }
    if (touched.confirmPassword && confirmPassword) {
      setConfirmPasswordError(validateConfirmPassword(confirmPassword, val));
    }
    if (localError) setLocalError(null);
  };

  const handleConfirmPasswordChange = (val: string) => {
    setConfirmPassword(val);
    if (touched.confirmPassword) {
      setConfirmPasswordError(validateConfirmPassword(val, password));
    }
    if (localError) setLocalError(null);
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    clearError();

    const uError = validateUsername(username);
    if (uError) {
      setTouched((prev) => ({ ...prev, username: true }));
      setUsernameError(uError);
      return;
    }

    const pError = validatePassword(password);
    if (pError) {
      setTouched((prev) => ({ ...prev, password: true }));
      setPasswordError(pError);
      return;
    }

    const cError = validateConfirmPassword(confirmPassword, password);
    if (cError) {
      setTouched((prev) => ({ ...prev, confirmPassword: true }));
      setConfirmPasswordError(cError);
      return;
    }

    const trimmedUser = username.trim();
    setIsSubmitting(true);
    try {
      await initAdmin({
        username: trimmedUser,
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
      icon={<KeyRound size={20} aria-hidden="true" />}
      title={t("auth.setupTitle")}
      subtitle={t("auth.setupSubtitle")}
      error={activeError}
    >
      <form onSubmit={handleSubmit} noValidate className="space-y-3.5">
        <div className="space-y-1">
          <label
            htmlFor="init-username"
            className="block text-xs font-medium text-[var(--muted)]"
          >
            {t("auth.username")}
          </label>
          <div className="relative flex items-center">
            <span className="pointer-events-none absolute left-3 text-[var(--muted)]">
              <UserIcon size={14} aria-hidden="true" />
            </span>
            <input
              id="init-username"
              name="username"
              type="text"
              autoComplete="username"
              required
              disabled={isSubmitting}
              value={username}
              onBlur={handleUsernameBlur}
              onChange={(e) => handleUsernameChange(e.target.value)}
              placeholder={t("auth.usernamePlaceholder")}
              aria-invalid={Boolean(usernameError)}
              aria-describedby={usernameError ? "init-username-error" : undefined}
              className={`h-9 sm:h-10 w-full rounded-xl bg-[var(--surface-muted)] py-1.5 pr-3 pl-9 text-[13px] text-[var(--foreground)] placeholder:text-[var(--muted)]/50 outline-none transition-all duration-200 disabled:opacity-60 ${
                usernameError
                  ? "ring-1 ring-[#ff3b30] focus:ring-2 focus:ring-[#ff3b30] bg-[#ff3b30]/[0.02]"
                  : "ring-1 ring-black/[0.05] dark:ring-white/[0.08] hover:ring-black/[0.12] dark:hover:ring-white/[0.15] focus:bg-[var(--surface)] focus:ring-2 focus:ring-[#0071e3]"
              }`}
            />
          </div>
          {usernameError && (
            <p
              id="init-username-error"
              role="alert"
              className="flex items-center gap-1 pt-0.5 text-[11px] font-medium text-[#ff3b30]"
            >
              <AlertCircle size={12} className="shrink-0" aria-hidden="true" />
              <span>{usernameError}</span>
            </p>
          )}
        </div>

        <div className="space-y-1">
          <label
            htmlFor="init-password"
            className="block text-xs font-medium text-[var(--muted)]"
          >
            {t("auth.password")}
          </label>
          <div className="relative flex items-center">
            <span className="pointer-events-none absolute left-3 text-[var(--muted)]">
              <Lock size={14} aria-hidden="true" />
            </span>
            <input
              id="init-password"
              name="password"
              type="password"
              autoComplete="new-password"
              required
              disabled={isSubmitting}
              value={password}
              onBlur={handlePasswordBlur}
              onChange={(e) => handlePasswordChange(e.target.value)}
              placeholder={t("auth.passwordPlaceholder")}
              aria-invalid={Boolean(passwordError)}
              aria-describedby={passwordError ? "init-password-error" : undefined}
              className={`h-9 sm:h-10 w-full rounded-xl bg-[var(--surface-muted)] py-1.5 pr-3 pl-9 text-[13px] text-[var(--foreground)] placeholder:text-[var(--muted)]/50 outline-none transition-all duration-200 disabled:opacity-60 ${
                passwordError
                  ? "ring-1 ring-[#ff3b30] focus:ring-2 focus:ring-[#ff3b30] bg-[#ff3b30]/[0.02]"
                  : "ring-1 ring-black/[0.05] dark:ring-white/[0.08] hover:ring-black/[0.12] dark:hover:ring-white/[0.15] focus:bg-[var(--surface)] focus:ring-2 focus:ring-[#0071e3]"
              }`}
            />
          </div>
          {strength && (
            <div className="pt-1 space-y-1">
              <div className="flex h-1 w-full gap-1 overflow-hidden" aria-hidden="true">
                <div
                  className={`h-full flex-1 rounded-full transition-colors duration-300 ${
                    strength === "weak"
                      ? "bg-[#ff3b30]"
                      : strength === "medium"
                      ? "bg-[#0071e3]"
                      : "bg-[#34c759]"
                  }`}
                />
                <div
                  className={`h-full flex-1 rounded-full transition-colors duration-300 ${
                    strength === "medium"
                      ? "bg-[#0071e3]"
                      : strength === "strong"
                      ? "bg-[#34c759]"
                      : "bg-black/[0.06] dark:bg-white/[0.1]"
                  }`}
                />
                <div
                  className={`h-full flex-1 rounded-full transition-colors duration-300 ${
                    strength === "strong"
                      ? "bg-[#34c759]"
                      : "bg-black/[0.06] dark:bg-white/[0.1]"
                  }`}
                />
              </div>
              <div className="flex items-center justify-between text-[11px]">
                <span className="text-[var(--muted)]">
                  {t("auth.passwordStrength", { strength: strengthLabel })}
                </span>
                <span className={`font-semibold ${strengthColors[strength]}`}>
                  {strengthLabel}
                </span>
              </div>
            </div>
          )}
          {passwordError && (
            <p
              id="init-password-error"
              role="alert"
              className="flex items-center gap-1 pt-0.5 text-[11px] font-medium text-[#ff3b30]"
            >
              <AlertCircle size={12} className="shrink-0" aria-hidden="true" />
              <span>{passwordError}</span>
            </p>
          )}
        </div>

        <div className="space-y-1">
          <label
            htmlFor="init-confirm-password"
            className="block text-xs font-medium text-[var(--muted)]"
          >
            {t("auth.confirmPassword")}
          </label>
          <div className="relative flex items-center">
            <span className="pointer-events-none absolute left-3 text-[var(--muted)]">
              <Lock size={14} aria-hidden="true" />
            </span>
            <input
              id="init-confirm-password"
              name="confirmPassword"
              type="password"
              autoComplete="new-password"
              required
              disabled={isSubmitting}
              value={confirmPassword}
              onBlur={handleConfirmPasswordBlur}
              onChange={(e) => handleConfirmPasswordChange(e.target.value)}
              placeholder={t("auth.confirmPasswordPlaceholder")}
              aria-invalid={Boolean(confirmPasswordError)}
              aria-describedby={confirmPasswordError ? "init-confirm-password-error" : undefined}
              className={`h-9 sm:h-10 w-full rounded-xl bg-[var(--surface-muted)] py-1.5 pr-3 pl-9 text-[13px] text-[var(--foreground)] placeholder:text-[var(--muted)]/50 outline-none transition-all duration-200 disabled:opacity-60 ${
                confirmPasswordError
                  ? "ring-1 ring-[#ff3b30] focus:ring-2 focus:ring-[#ff3b30] bg-[#ff3b30]/[0.02]"
                  : "ring-1 ring-black/[0.05] dark:ring-white/[0.08] hover:ring-black/[0.12] dark:hover:ring-white/[0.15] focus:bg-[var(--surface)] focus:ring-2 focus:ring-[#0071e3]"
              }`}
            />
          </div>
          {confirmPasswordError && (
            <p
              id="init-confirm-password-error"
              role="alert"
              className="flex items-center gap-1 pt-0.5 text-[11px] font-medium text-[#ff3b30]"
            >
              <AlertCircle size={12} className="shrink-0" aria-hidden="true" />
              <span>{confirmPasswordError}</span>
            </p>
          )}
        </div>

        <button
          type="submit"
          disabled={isSubmitting}
          className="mt-5 flex h-9 sm:h-10 w-full items-center justify-center gap-1.5 rounded-full bg-[#0071e3] px-5 text-[13px] font-medium text-white transition-all duration-200 ease-[cubic-bezier(0.25,0.1,0.25,1)] hover:bg-[#0077ed] active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed disabled:active:scale-100 shadow-[0_1px_2px_rgba(0,113,227,0.2)]"
        >
          {isSubmitting ? (
            <>
              <LoaderCircle size={15} className="animate-spin" aria-hidden="true" />
              <span>{t("auth.submitting")}</span>
            </>
          ) : (
            <>
              <CheckCircle size={15} aria-hidden="true" />
              <span>{t("auth.initButton")}</span>
            </>
          )}
        </button>
      </form>
    </AuthCard>
  );
}
