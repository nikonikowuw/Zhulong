import { useTranslation } from "react-i18next";
import { LogOut, User as UserIcon } from "lucide-react";
import { useAuth } from "../hooks/useAuth";

export function UserNav() {
  const { user, phase, logout } = useAuth();
  const { t } = useTranslation();

  if (phase !== "authenticated" || !user) {
    return null;
  }

  return (
    <div className="mr-2 flex items-center gap-2">
      <span className="inline-flex items-center gap-1.5 rounded-full border border-[var(--border)] bg-[var(--surface-muted)] px-3 py-1 text-xs font-medium text-[var(--foreground)]">
        <UserIcon size={13} aria-hidden="true" />
        <span>{user.username}</span>
      </span>
      <button
        type="button"
        onClick={() => void logout()}
        className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-2.5 py-1 text-xs font-medium text-[var(--foreground)] transition hover:bg-[var(--surface-hover)]"
        aria-label={t("auth.logoutButton")}
        title={t("auth.logoutButton")}
      >
        <LogOut size={13} aria-hidden="true" />
        <span className="hidden sm:inline">{t("auth.logoutButton")}</span>
      </button>
    </div>
  );
}
