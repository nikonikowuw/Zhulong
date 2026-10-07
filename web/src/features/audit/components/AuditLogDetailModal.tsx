import { useEffect, useState } from "react";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Clock,
  Copy,
  Globe,
  ScrollText,
  User,
  X,
  XCircle,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { AuditLogItem } from "../types";

export interface AuditLogDetailModalProps {
  isOpen: boolean;
  onClose: () => void;
  log: AuditLogItem | null;
}

function formatJsonDetail(raw: string): string {
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return raw;
  }
}

export function AuditLogDetailModal({
  isOpen,
  onClose,
  log,
}: AuditLogDetailModalProps) {
  const { t } = useTranslation();
  const [copiedLogId, setCopiedLogId] = useState<number | null>(null);

  useEffect(() => {
    if (!isOpen) return;
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        onClose();
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen || !log) return null;

  const isCopied = copiedLogId === log.id;
  const isSuccess = log.status === "success";
  const formattedDetail = formatJsonDetail(log.detail);

  async function handleCopy() {
    if (!formattedDetail || !log) return;
    try {
      await navigator.clipboard.writeText(formattedDetail);
      setCopiedLogId(log.id);
      setTimeout(() => setCopiedLogId(null), 2000);
    } catch {
      // clipboard write failed
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150"
      role="dialog"
      aria-modal="true"
      aria-labelledby="audit-detail-title"
    >
      <div className="w-full max-w-2xl rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-2xl overflow-hidden flex flex-col max-h-[88vh]">
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-[var(--border)]">
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-[var(--accent)]/10 text-[var(--accent)]">
              <ScrollText size={18} />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h2 id="audit-detail-title" className="text-base font-bold text-[var(--foreground)]">
                  {t("audit.modal.title")}
                </h2>
                <span className="rounded-md bg-[var(--surface-muted)] px-1.5 py-0.5 font-mono text-[11px] font-semibold text-[var(--muted)] border border-[var(--border)]">
                  #{log.id}
                </span>
              </div>
              <p className="text-[11px] text-[var(--muted)]">
                {new Date(log.createdAt).toLocaleString()}
              </p>
            </div>
          </div>

          <div className="flex items-center gap-3">
            {/* Status Pill */}
            {isSuccess ? (
              <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
                <CheckCircle2 size={13} className="text-emerald-500" />
                <span>{t("audit.filter.success")}</span>
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border border-rose-500/20 bg-rose-500/10 text-rose-600 dark:text-rose-400">
                <XCircle size={13} className="text-rose-500" />
                <span>{t("audit.filter.failed")}</span>
              </span>
            )}

            <button
              type="button"
              className="p-1 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors cursor-pointer"
              onClick={onClose}
              aria-label={t("audit.modal.close")}
            >
              <X size={18} />
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="py-4 space-y-4 overflow-y-auto pr-1">
          {/* Metadata Grid */}
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 text-xs">
            {/* Action */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.action")}</span>
              <span className="font-mono text-[var(--foreground)] font-semibold text-[11px] break-all">
                {log.action}
              </span>
            </div>

            {/* Operator */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.operator")}</span>
              <span className="inline-flex items-center gap-1.5 text-[var(--foreground)] font-medium">
                <User size={13} className="text-[var(--muted)]" />
                <span>{log.username || "admin"}</span>
              </span>
            </div>

            {/* Client IP */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.ip")}</span>
              <span className="font-mono text-[var(--foreground)] inline-flex items-center gap-1.5">
                <Globe size={13} className="text-[var(--muted)]" />
                <span>{log.ip || "-"}</span>
              </span>
            </div>

            {/* Target */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.target")}</span>
              <span className="font-mono text-[var(--foreground)] break-all font-medium">
                {log.target || "-"}
              </span>
            </div>

            {/* Execution Status */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.status")}</span>
              <span className={`font-semibold ${isSuccess ? "text-emerald-600 dark:text-emerald-400" : "text-[var(--danger)]"}`}>
                {isSuccess ? t("audit.filter.success") : t("audit.filter.failed")}
              </span>
            </div>

            {/* Recorded Time */}
            <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
              <span className="text-[var(--muted)] font-medium block mb-1">{t("audit.modal.time")}</span>
              <span className="font-mono text-[var(--foreground)] text-[11px] inline-flex items-center gap-1">
                <Clock size={12} className="text-[var(--muted)]" />
                <span>{new Date(log.createdAt).toLocaleTimeString()}</span>
              </span>
            </div>
          </div>

          {/* Failure Alert Box */}
          {log.errorMsg && (
            <div className="rounded-xl border border-[var(--danger)]/30 bg-[var(--danger)]/10 p-3.5 text-xs text-[var(--danger)]">
              <div className="flex items-center gap-1.5 font-bold mb-1">
                <AlertTriangle size={15} />
                <span>{t("audit.modal.errorMsg")}:</span>
              </div>
              <p className="font-mono break-all text-[11px] leading-relaxed pl-5">
                {log.errorMsg}
              </p>
            </div>
          )}

          {/* Detail Parameters Box */}
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-[var(--foreground)]">
                {t("audit.modal.detail")}
              </span>
              {formattedDetail && (
                <button
                  type="button"
                  onClick={handleCopy}
                  className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-xs font-medium text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] border border-[var(--border)] transition-colors cursor-pointer"
                  title={t("audit.modal.copyJson", "复制 JSON")}
                >
                  {isCopied ? (
                    <>
                      <Check size={12} className="text-emerald-500" />
                      <span className="text-emerald-500 font-semibold">{t("audit.modal.copied", "已复制")}</span>
                    </>
                  ) : (
                    <>
                      <Copy size={12} />
                      <span>{t("audit.modal.copyJson", "复制 JSON")}</span>
                    </>
                  )}
                </button>
              )}
            </div>

            {formattedDetail ? (
              <pre className="rounded-xl border border-[var(--border)] bg-[#1e1e20] dark:bg-[#141416] text-[#e5e5ea] p-4 text-xs font-mono overflow-x-auto max-h-56 whitespace-pre-wrap break-all shadow-inner">
                {formattedDetail}
              </pre>
            ) : (
              <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-6 text-center text-xs text-[var(--muted)]">
                {t("audit.modal.noDetail")}
              </div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end pt-3 border-t border-[var(--border)]">
          <button
            type="button"
            className="rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors cursor-pointer"
            onClick={onClose}
          >
            {t("audit.modal.close")}
          </button>
        </div>
      </div>
    </div>
  );
};
