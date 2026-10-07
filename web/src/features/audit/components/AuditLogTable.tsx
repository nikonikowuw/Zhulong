import {
  Activity,
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Eye,
  Globe,
  KeyRound,
  LogOut,
  ScrollText,
  ShieldAlert,
  ShieldCheck,
  Sliders,
  ToggleRight,
  Trash2,
  User,
  Video,
  type LucideIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { AuditLogItem } from "../types";

export interface AuditLogTableProps {
  items: AuditLogItem[];
  isLoading: boolean;
  error?: unknown;
  onRetry?: () => void;
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (newPage: number) => void;
  onPageSizeChange: (newSize: number) => void;
  onSelectLog: (log: AuditLogItem) => void;
  onClearFilters?: () => void;
}

interface ActionBadgeConfig {
  label: string;
  icon: LucideIcon;
  badgeClass: string;
  iconClass: string;
}

export function AuditLogTable({
  items,
  isLoading,
  error,
  onRetry,
  total,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
  onSelectLog,
  onClearFilters,
}: AuditLogTableProps) {
  const { t } = useTranslation();

  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  function getActionBadge(action: string, isSuccess: boolean): ActionBadgeConfig {
    switch (action) {
      case "auth.login":
        return isSuccess
          ? {
              label: t("audit.actions.auth_login", "管理员登录"),
              icon: ShieldCheck,
              badgeClass: "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
              iconClass: "text-emerald-500",
            }
          : {
              label: t("audit.actions.auth_login", "管理员登录"),
              icon: ShieldAlert,
              badgeClass: "border-rose-500/20 bg-rose-500/10 text-rose-700 dark:text-rose-300",
              iconClass: "text-rose-500",
            };
      case "auth.logout":
        return {
          label: t("audit.actions.auth_logout", "管理员登出"),
          icon: LogOut,
          badgeClass: "border-slate-500/20 bg-slate-500/10 text-slate-700 dark:text-slate-300",
          iconClass: "text-slate-500",
        };
      case "auth.init":
        return {
          label: t("audit.actions.auth_init", "初始化管理员"),
          icon: KeyRound,
          badgeClass: "border-indigo-500/20 bg-indigo-500/10 text-indigo-700 dark:text-indigo-300",
          iconClass: "text-indigo-500",
        };
      case "camera.create":
        return {
          label: t("audit.actions.camera_create", "添加摄像头"),
          icon: Video,
          badgeClass: "border-cyan-500/20 bg-cyan-500/10 text-cyan-700 dark:text-cyan-300",
          iconClass: "text-cyan-500",
        };
      case "camera.update":
        return {
          label: t("audit.actions.camera_update", "修改摄像头"),
          icon: Sliders,
          badgeClass: "border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300",
          iconClass: "text-amber-500",
        };
      case "camera.delete":
        return {
          label: t("audit.actions.camera_delete", "删除摄像头"),
          icon: Trash2,
          badgeClass: "border-rose-500/20 bg-rose-500/10 text-rose-700 dark:text-rose-300",
          iconClass: "text-rose-500",
        };
      case "camera.toggle":
        return {
          label: t("audit.actions.camera_toggle", "启停摄像头"),
          icon: ToggleRight,
          badgeClass: "border-violet-500/20 bg-violet-500/10 text-violet-700 dark:text-violet-300",
          iconClass: "text-violet-500",
        };
      default:
        return {
          label: action,
          icon: Activity,
          badgeClass: "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--foreground)]",
          iconClass: "text-[var(--muted)]",
        };
    }
  }

  function formatTime(isoString: string): string {
    const d = new Date(isoString);
    return Number.isNaN(d.getTime()) ? isoString : d.toLocaleString();
  }

  function renderTableBody() {
    if (isLoading) {
      return (
        <tr>
          <td colSpan={7} className="py-20 text-center text-[var(--muted)]">
            <div className="flex flex-col items-center justify-center gap-3" role="status">
              <span className="inline-block h-6 w-6 animate-spin rounded-full border-2 border-[var(--accent)] border-t-transparent" />
              <span className="text-xs font-medium">{t("audit.table.loading")}</span>
            </div>
          </td>
        </tr>
      );
    }

    if (error) {
      return (
        <tr>
          <td colSpan={7} className="py-20 text-center" role="alert">
            <div className="flex flex-col items-center justify-center gap-2.5 text-[var(--danger)]">
              <AlertTriangle size={24} aria-hidden="true" />
              <span className="font-semibold text-xs">
                {error instanceof Error ? error.message : t("audit.table.loadError")}
              </span>
              {onRetry && (
                <button
                  type="button"
                  onClick={onRetry}
                  className="mt-3 inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
                >
                  {t("audit.table.retry")}
                </button>
              )}
            </div>
          </td>
        </tr>
      );
    }

    if (items.length === 0) {
      return (
        <tr>
          <td colSpan={7} className="py-20 text-center text-[var(--muted)]">
            <div className="flex flex-col items-center justify-center gap-2">
              <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-[var(--surface-muted)] text-[var(--muted)]">
                <ScrollText size={20} aria-hidden="true" />
              </div>
              <span className="text-sm font-semibold text-[var(--foreground)] mt-1">
                {t("audit.table.empty")}
              </span>
              <span className="text-xs text-[var(--muted)] max-w-sm">
                {t("audit.table.emptyDesc", "当系统发生管理员认证、设备配置变更或异常事件时，将自动记录并在此展示。")}
              </span>
              {onClearFilters && (
                <button
                  type="button"
                  onClick={onClearFilters}
                  className="mt-3 inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
                >
                  {t("audit.filter.resetFilters", "重置筛选条件")}
                </button>
              )}
            </div>
          </td>
        </tr>
      );
    }

    return items.map((log) => {
      const isSuccess = log.status === "success";
      const badge = getActionBadge(log.action, isSuccess);
      const BadgeIcon = badge.icon;

      return (
        <tr
          key={log.id}
          className="hover:bg-[var(--surface-hover)]/70 transition-colors group cursor-pointer"
          onClick={() => onSelectLog(log)}
        >
          {/* Time */}
          <td className="py-3 px-4 font-mono text-[var(--muted)] whitespace-nowrap text-[11px]">
            {formatTime(log.createdAt)}
          </td>

          {/* Action Badge */}
          <td className="py-3 px-4 whitespace-nowrap">
            <span
              className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-[11px] font-medium border ${badge.badgeClass}`}
            >
              <BadgeIcon size={13} className={badge.iconClass} aria-hidden="true" />
              <span>{badge.label}</span>
            </span>
          </td>

          {/* Target */}
          <td className="py-3 px-4">
            {log.target ? (
              <span className="inline-flex items-center px-2 py-0.5 rounded-md font-mono text-[11px] bg-[var(--surface-muted)] text-[var(--foreground)] border border-[var(--border)]">
                {log.target}
              </span>
            ) : (
              <span className="text-[var(--muted)]">-</span>
            )}
          </td>

          {/* Operator */}
          <td className="py-3 px-4 whitespace-nowrap">
            <span className="inline-flex items-center gap-1.5 text-[var(--foreground)] font-medium">
              <User size={12} className="text-[var(--muted)]" aria-hidden="true" />
              <span>{log.username || "admin"}</span>
            </span>
          </td>

          {/* Client IP */}
          <td className="py-3 px-4 font-mono text-[11px] text-[var(--muted)] whitespace-nowrap">
            <span className="inline-flex items-center gap-1.5">
              <Globe size={12} className="text-[var(--muted)]" aria-hidden="true" />
              <span>{log.ip || "-"}</span>
            </span>
          </td>

          {/* Execution Status */}
          <td className="py-3 px-4 text-center whitespace-nowrap">
            {isSuccess ? (
              <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[11px] font-medium border border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden="true" />
                <span>{t("audit.filter.success")}</span>
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[11px] font-medium border border-rose-500/20 bg-rose-500/10 text-rose-600 dark:text-rose-400">
                <span className="h-1.5 w-1.5 rounded-full bg-rose-500 animate-pulse" aria-hidden="true" />
                <span>{t("audit.filter.failed")}</span>
              </span>
            )}
          </td>

          {/* Details Action */}
          <td className="py-3 px-4 text-right whitespace-nowrap">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onSelectLog(log);
              }}
              className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-[var(--accent)] hover:bg-[var(--accent)]/10 transition-colors"
              title={t("audit.table.details")}
            >
              <Eye size={13} aria-hidden="true" />
              <span>{t("audit.table.viewDetails")}</span>
            </button>
          </td>
        </tr>
      );
    });
  }

  return (
    <div className="flex flex-1 min-h-0 flex-col rounded-2xl border border-[var(--border)] bg-[var(--surface)] overflow-hidden shadow-xs">
      {/* Table Container */}
      <div className="flex-1 overflow-auto min-h-0">
        <table className="w-full text-left border-collapse text-xs">
          <thead className="sticky top-0 z-10">
            <tr className="border-b border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)] font-medium">
              <th className="py-3 px-4 font-semibold">{t("audit.table.time")}</th>
              <th className="py-3 px-4 font-semibold">{t("audit.table.action")}</th>
              <th className="py-3 px-4 font-semibold">{t("audit.table.target")}</th>
              <th className="py-3 px-4 font-semibold">{t("audit.table.operator")}</th>
              <th className="py-3 px-4 font-semibold">{t("audit.table.ip")}</th>
              <th className="py-3 px-4 text-center font-semibold">{t("audit.table.status")}</th>
              <th className="py-3 px-4 text-right font-semibold">{t("audit.table.details")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--border)] text-[var(--foreground)]">
            {renderTableBody()}
          </tbody>
        </table>
      </div>

      {/* Pagination Footer */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 border-t border-[var(--border)] bg-[var(--surface-muted)]/50 px-4 py-3 text-xs text-[var(--muted)] shrink-0">
        {/* Left: Total & Page Size */}
        <div className="flex items-center gap-3">
          <span>{t("audit.table.pagination.totalCount", { total })}</span>
          <div className="flex items-center gap-1.5">
            <select
              aria-label={t("audit.table.pagination.pageSizeLabel", "每页条数")}
              value={pageSize}
              onChange={(e) => onPageSizeChange(Number(e.target.value))}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-2 py-1 text-xs text-[var(--foreground)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] cursor-pointer"
            >
              {[10, 20, 50, 100].map((size) => (
                <option key={size} value={size}>
                  {t("audit.table.pagination.pageSize", { size })}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Right: Page Navigation */}
        <div className="flex items-center gap-2 self-end sm:self-auto">
          <span>
            {t("audit.table.pagination.pageInfo", { current: page, total: totalPages })}
          </span>
          <div className="flex items-center gap-1">
            <button
              type="button"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
              className="flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--surface)] text-[var(--foreground)] hover:bg-[var(--surface-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors shadow-2xs"
              aria-label={t("audit.table.pagination.prev")}
              title={t("audit.table.pagination.prev")}
            >
              <ChevronLeft size={15} aria-hidden="true" />
            </button>
            <button
              type="button"
              disabled={page >= totalPages}
              onClick={() => onPageChange(page + 1)}
              className="flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--surface)] text-[var(--foreground)] hover:bg-[var(--surface-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition-colors shadow-2xs"
              aria-label={t("audit.table.pagination.next")}
              title={t("audit.table.pagination.next")}
            >
              <ChevronRight size={15} aria-hidden="true" />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
