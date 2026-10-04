import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, LoaderCircle, Lock, LogIn, User as UserIcon } from "lucide-react";
import { useAuth } from "../hooks/useAuth";
import { AuthCard } from "./AuthCard";

export function LoginForm() {
  const { t } = useTranslation();
  const { login, error, clearError } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [touched, setTouched] = useState({ username: false, password: false });
  const [usernameError, setUsernameError] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
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
    return null;
  };

  const handleUsernameBlur = () => {
    setTouched((prev) => ({ ...prev, username: true }));
    setUsernameError(validateUsername(username));
  };

  const handlePasswordBlur = () => {
    setTouched((prev) => ({ ...prev, password: true }));
    setPasswordError(validatePassword(password));
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

    const trimmedUser = username.trim();
    setIsSubmitting(true);
    try {
      await login({
        username: trimmedUser,
        password,
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

  return (
    <AuthCard
      icon={<Lock size={20} aria-hidden="true" />}
      title={t("auth.loginTitle")}
      subtitle={t("auth.loginSubtitle")}
      error={activeError}
    >
      <form onSubmit={handleSubmit} noValidate className="space-y-3.5">
        <div className="space-y-1">
          <label
            htmlFor="login-username"
            className="block text-xs font-medium text-[var(--muted)]"
          >
            {t("auth.username")}
          </label>
          <div className="relative flex items-center">
            <span className="pointer-events-none absolute left-3 text-[var(--muted)]">
              <UserIcon size={14} aria-hidden="true" />
            </span>
            <input
              id="login-username"
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
              aria-describedby={usernameError ? "login-username-error" : undefined}
              className={`h-9 sm:h-10 w-full rounded-xl bg-[var(--surface-muted)] py-1.5 pr-3 pl-9 text-[13px] text-[var(--foreground)] placeholder:text-[var(--muted)]/50 outline-none transition-all duration-200 disabled:opacity-60 ${
                usernameError
                  ? "ring-1 ring-[#ff3b30] focus:ring-2 focus:ring-[#ff3b30] bg-[#ff3b30]/[0.02]"
                  : "ring-1 ring-black/[0.05] dark:ring-white/[0.08] hover:ring-black/[0.12] dark:hover:ring-white/[0.15] focus:bg-[var(--surface)] focus:ring-2 focus:ring-[#0071e3]"
              }`}
            />
          </div>
          {usernameError && (
            <p
              id="login-username-error"
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
            htmlFor="login-password"
            className="block text-xs font-medium text-[var(--muted)]"
          >
            {t("auth.password")}
          </label>
          <div className="relative flex items-center">
            <span className="pointer-events-none absolute left-3 text-[var(--muted)]">
              <Lock size={14} aria-hidden="true" />
            </span>
            <input
              id="login-password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              disabled={isSubmitting}
              value={password}
              onBlur={handlePasswordBlur}
              onChange={(e) => handlePasswordChange(e.target.value)}
              placeholder={t("auth.passwordPlaceholder")}
              aria-invalid={Boolean(passwordError)}
              aria-describedby={passwordError ? "login-password-error" : undefined}
              className={`h-9 sm:h-10 w-full rounded-xl bg-[var(--surface-muted)] py-1.5 pr-3 pl-9 text-[13px] text-[var(--foreground)] placeholder:text-[var(--muted)]/50 outline-none transition-all duration-200 disabled:opacity-60 ${
                passwordError
                  ? "ring-1 ring-[#ff3b30] focus:ring-2 focus:ring-[#ff3b30] bg-[#ff3b30]/[0.02]"
                  : "ring-1 ring-black/[0.05] dark:ring-white/[0.08] hover:ring-black/[0.12] dark:hover:ring-white/[0.15] focus:bg-[var(--surface)] focus:ring-2 focus:ring-[#0071e3]"
              }`}
            />
          </div>
          {passwordError && (
            <p
              id="login-password-error"
              role="alert"
              className="flex items-center gap-1 pt-0.5 text-[11px] font-medium text-[#ff3b30]"
            >
              <AlertCircle size={12} className="shrink-0" aria-hidden="true" />
              <span>{passwordError}</span>
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
              <LogIn size={15} aria-hidden="true" />
              <span>{t("auth.loginButton")}</span>
            </>
          )}
        </button>
      </form>
    </AuthCard>
  );
}
