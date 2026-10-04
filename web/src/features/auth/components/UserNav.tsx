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
    <div className="mr-1 flex items-center gap-1.5">
      <span className="inline-flex h-8 items-center gap-1.5 rounded-full bg-[var(--surface-muted)] px-3 text-xs font-medium text-[var(--foreground)] border-0 transition-colors">
        <UserIcon size={12} className="text-[var(--muted)]" aria-hidden="true" />
        <span>{user.username}</span>
      </span>
      <button
        type="button"
        onClick={() => void logout()}
        className="inline-flex h-8 items-center gap-1.5 rounded-full bg-[var(--surface-muted)] px-2.5 text-xs font-medium text-[var(--foreground)] border-0 transition-all duration-200 ease-[cubic-bezier(0.25,0.1,0.25,1)] hover:bg-[var(--surface-hover)] active:scale-95"
        aria-label={t("auth.logoutButton")}
        title={t("auth.logoutButton")}
      >
        <LogOut size={12} aria-hidden="true" />
        <span className="hidden sm:inline">{t("auth.logoutButton")}</span>
      </button>
    </div>
  );
}
