import { Filter, RefreshCw, Search, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface AuditLogFilterBarProps {
  search?: string;
  onSearchChange?: (val: string) => void;
  action: string;
  onActionChange: (action: string) => void;
  status: string;
  onStatusChange: (status: string) => void;
  onRefresh?: () => void;
  isRefreshing?: boolean;
  onOpenClearModal?: () => void;
  className?: string;
}

const ACTION_OPTIONS = [
  { value: "", labelKey: "audit.filter.allActions" },
  { value: "auth.init", labelKey: "audit.actions.auth_init" },
  { value: "auth.login", labelKey: "audit.actions.auth_login" },
  { value: "auth.logout", labelKey: "audit.actions.auth_logout" },
  { value: "camera.create", labelKey: "audit.actions.camera_create" },
  { value: "camera.update", labelKey: "audit.actions.camera_update" },
  { value: "camera.delete", labelKey: "audit.actions.camera_delete" },
  { value: "camera.toggle", labelKey: "audit.actions.camera_toggle" },
];

const STATUS_OPTIONS = [
  { value: "", labelKey: "audit.filter.allStatuses" },
  { value: "success", labelKey: "audit.filter.success" },
  { value: "failed", labelKey: "audit.filter.failed" },
];

export function AuditLogFilterBar({
  search = "",
  onSearchChange,
  action,
  onActionChange,
  status,
  onStatusChange,
  onRefresh,
  isRefreshing = false,
  onOpenClearModal,
  className = "mb-5",
}: AuditLogFilterBarProps) {
  const { t } = useTranslation();

  return (
    <div className={`flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 ${className} shrink-0`}>
      {/* Left: Search & Filter Dropdowns */}
      <div className="flex flex-1 flex-wrap items-center gap-2.5">
        {/* Search Input */}
        {onSearchChange && (
          <div className="relative min-w-[200px] max-w-full sm:max-w-xs flex-1">
            <Search
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--muted)]"
              size={15}
              aria-hidden="true"
            />
            <input
              id="audit-search-input"
              type="text"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              placeholder={t("audit.filter.searchPlaceholder", "搜索动作、用户、目标或 IP...")}
              className="w-full rounded-xl border border-[var(--border)] bg-[var(--surface)] py-1.5 pl-8 pr-7 text-xs text-[var(--foreground)] placeholder:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] transition-all shadow-2xs"
            />
            {search && (
              <button
                type="button"
                onClick={() => onSearchChange("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-[var(--muted)] hover:text-[var(--foreground)] rounded-full transition-colors cursor-pointer"
                aria-label={t("audit.filter.clearSearch", "清除搜索")}
              >
                <X size={13} aria-hidden="true" />
              </button>
            )}
          </div>
        )}

        {/* Action Filter */}
        <div className="flex items-center gap-1.5">
          <Filter size={13} className="text-[var(--muted)]" aria-hidden="true" />
          <select
            aria-label={t("audit.filter.action")}
            value={action}
            onChange={(e) => onActionChange(e.target.value)}
            className="rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--foreground)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] transition-all cursor-pointer shadow-2xs"
          >
            {ACTION_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {t(opt.labelKey)}
              </option>
            ))}
          </select>
        </div>

        {/* Status Filter */}
        <div className="flex items-center gap-1.5">
          <select
            aria-label={t("audit.filter.status")}
            value={status}
            onChange={(e) => onStatusChange(e.target.value)}
            className="rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--foreground)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] transition-all cursor-pointer shadow-2xs"
          >
            {STATUS_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {t(opt.labelKey)}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* Right: Actions (rendered when onRefresh or onOpenClearModal provided) */}
      {(onRefresh || onOpenClearModal) && (
        <div className="flex items-center gap-2 self-end sm:self-auto shrink-0">
          {onRefresh && (
            <button
              type="button"
              onClick={onRefresh}
              disabled={isRefreshing}
              className="flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] active:scale-[0.98] transition-all disabled:opacity-50 cursor-pointer shadow-2xs"
              title={t("audit.filter.refresh")}
            >
              <RefreshCw size={13} className={isRefreshing ? "animate-spin" : ""} aria-hidden="true" />
              <span>{t("audit.filter.refresh")}</span>
            </button>
          )}

          {onOpenClearModal && (
            <button
              type="button"
              onClick={onOpenClearModal}
              className="flex items-center gap-1.5 rounded-xl border border-[var(--danger)]/30 bg-[var(--danger)]/10 px-3 py-1.5 text-xs font-medium text-[var(--danger)] hover:bg-[var(--danger)]/20 active:scale-[0.98] transition-all cursor-pointer"
              title={t("audit.filter.clearLogs")}
            >
              <Trash2 size={13} aria-hidden="true" />
              <span>{t("audit.filter.clearLogs")}</span>
            </button>
          )}
        </div>
      )}
    </div>
  );
};
