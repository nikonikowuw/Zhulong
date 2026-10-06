import { useTranslation } from "react-i18next";
import { LogOut, User as UserIcon } from "lucide-react";
import { useAuth } from "../hooks/useAuth";
import type { FC } from "react";

interface UserNavProps {
  isCollapsed?: boolean;
}

export const UserNav: FC<UserNavProps> = ({ isCollapsed = false }) => {
  const { user, phase, logout } = useAuth();
  const { t } = useTranslation();

  if (phase !== "authenticated" || !user) {
    return null;
  }

  if (isCollapsed) {
    return (
      <div className="flex w-full items-center justify-center">
        <button
          type="button"
          onClick={() => void logout()}
          className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--muted)] hover:bg-[var(--danger-soft)] hover:text-[var(--danger)] transition-colors"
          aria-label={t("auth.logoutButton")}
          title={`${user.username} (${t("auth.logoutButton")})`}
        >
          <LogOut size={16} aria-hidden="true" />
        </button>
      </div>
    );
  }

  return (
    <div className="flex w-full items-center justify-between rounded-lg bg-[var(--surface-muted)] px-2.5 py-1.5 text-xs text-[var(--foreground)] border border-[var(--border)]">
      <div className="flex items-center gap-2 min-w-0">
        <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[var(--surface-hover)] text-[var(--muted)]">
          <UserIcon size={13} aria-hidden="true" />
        </div>
        <span className="truncate font-medium">{user.username}</span>
      </div>
      <button
        type="button"
        onClick={() => void logout()}
        className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[var(--muted)] hover:bg-[var(--danger-soft)] hover:text-[var(--danger)] transition-colors"
        aria-label={t("auth.logoutButton")}
        title={t("auth.logoutButton")}
      >
        <LogOut size={13} aria-hidden="true" />
      </button>
    </div>
  );
};
