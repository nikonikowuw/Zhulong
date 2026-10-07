import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, HardDrive, RefreshCw, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAuditLogsQuery, useClearAuditLogsMutation } from "../hooks/useAuditLogs";
import type { AuditLogItem } from "../types";
import { AuditLogDetailModal } from "./AuditLogDetailModal";
import { AuditLogFilterBar } from "./AuditLogFilterBar";
import { AuditLogTable } from "./AuditLogTable";

export function AuditLogPage() {
  const { t } = useTranslation();

  const [search, setSearch] = useState<string>("");
  const [action, setAction] = useState<string>("");
  const [status, setStatus] = useState<string>("");
  const [page, setPage] = useState<number>(1);
  const [pageSize, setPageSize] = useState<number>(20);

  const [selectedLog, setSelectedLog] = useState<AuditLogItem | null>(null);
  const [isClearModalOpen, setIsClearModalOpen] = useState(false);
  const [clearError, setClearError] = useState<string | null>(null);

  const { data, isLoading, isFetching, error, refetch } = useAuditLogsQuery({
    page,
    pageSize,
    action: action || undefined,
    status: status || undefined,
  });

  const clearMutation = useClearAuditLogsMutation();

  useEffect(() => {
    if (!isClearModalOpen) return;
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape" && !clearMutation.isPending) {
        setIsClearModalOpen(false);
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isClearModalOpen, clearMutation.isPending]);

  function handleActionChange(newAction: string) {
    setAction(newAction);
    setPage(1);
  }

  function handleStatusChange(newStatus: string) {
    setStatus(newStatus);
    setPage(1);
  }

  function handlePageSizeChange(newSize: number) {
    setPageSize(newSize);
    setPage(1);
  }

  function handleClearAllFilters() {
    setSearch("");
    setAction("");
    setStatus("");
    setPage(1);
  }

  function handleConfirmClear() {
    setClearError(null);
    clearMutation.mutate(undefined, {
      onSuccess: () => {
        setIsClearModalOpen(false);
      },
      onError: (err) => {
        setClearError(err instanceof Error ? err.message : String(err));
      },
    });
  }

  const rawItems = useMemo(() => data?.items ?? [], [data?.items]);
  const total = data?.total ?? 0;

  // Filter items if keyword search is active
  const items = useMemo(() => {
    if (!search.trim()) return rawItems;
    const lower = search.toLowerCase();
    return rawItems.filter(
      (log) =>
        log.ip?.toLowerCase().includes(lower) ||
        log.username?.toLowerCase().includes(lower) ||
        log.target?.toLowerCase().includes(lower) ||
        log.action?.toLowerCase().includes(lower) ||
        log.detail?.toLowerCase().includes(lower),
    );
  }, [rawItems, search]);

  return (
    <div className="audit-management-view flex flex-1 flex-col min-h-0">
      {/* Page Header - Unified structure with CameraPage and OverviewDashboard */}
      <section
        className="page-heading flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-6"
        aria-labelledby="audit-page-title"
      >
        <div>
          <div className="flex items-center gap-2.5">
            <h1 id="audit-page-title" className="text-2xl font-bold tracking-tight text-[var(--foreground)]">
              {t("audit.title")}
            </h1>
            <span className="inline-flex items-center gap-1 rounded-full border border-[var(--border)] bg-[var(--surface-muted)] px-2.5 py-0.5 text-[11px] font-mono text-[var(--muted)]">
              <HardDrive size={11} aria-hidden="true" />
              <span>{total.toLocaleString()} / 5,000 (FIFO)</span>
            </span>
          </div>
          <p className="page-description text-xs text-[var(--muted)] mt-1">
            {t("audit.subtitle")}
          </p>
        </div>

        {/* Action buttons on the right of header - Matching CameraPage */}
        <div className="flex items-center gap-2.5">
          <button
            type="button"
            className="icon-button"
            onClick={() => void refetch()}
            disabled={isFetching}
            aria-label={t("audit.filter.refresh")}
            title={t("audit.filter.refresh")}
          >
            <RefreshCw className={isFetching ? "animate-spin" : undefined} size={16} />
          </button>

          <button
            type="button"
            onClick={() => {
              setClearError(null);
              setIsClearModalOpen(true);
            }}
            className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger)]/30 bg-[var(--danger)]/10 px-3.5 py-2 text-xs font-medium text-[var(--danger)] hover:bg-[var(--danger)]/20 active:scale-[0.98] transition-all cursor-pointer shadow-xs"
            title={t("audit.filter.clearLogs")}
          >
            <Trash2 size={15} aria-hidden="true" />
            <span>{t("audit.filter.clearLogs")}</span>
          </button>
        </div>
      </section>

      {/* Filter and Search Bar */}
      <AuditLogFilterBar
        search={search}
        onSearchChange={setSearch}
        action={action}
        onActionChange={handleActionChange}
        status={status}
        onStatusChange={handleStatusChange}
      />

      {/* Audit Log Table - Fills remaining viewport */}
      <AuditLogTable
        items={items}
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        total={total}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={handlePageSizeChange}
        onSelectLog={setSelectedLog}
        onClearFilters={search || action || status ? handleClearAllFilters : undefined}
      />

      {/* Detail Modal */}
      <AuditLogDetailModal
        isOpen={Boolean(selectedLog)}
        onClose={() => setSelectedLog(null)}
        log={selectedLog}
      />

      {/* Clear Logs Confirmation Modal */}
      {isClearModalOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150"
          role="dialog"
          aria-modal="true"
          aria-labelledby="clear-audit-modal-title"
        >
          <div className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-2xl flex flex-col gap-4">
            <div className="flex items-center justify-between pb-3 border-b border-[var(--border)]">
              <div className="flex items-center gap-2 text-[var(--danger)]">
                <Trash2 size={18} />
                <h3 id="clear-audit-modal-title" className="text-base font-semibold text-[var(--foreground)]">
                  {t("audit.clearModal.title")}
                </h3>
              </div>
              <button
                type="button"
                className="p-1 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors cursor-pointer"
                onClick={() => !clearMutation.isPending && setIsClearModalOpen(false)}
                aria-label={t("common.close", "Close")}
              >
                <X size={18} />
              </button>
            </div>

            <p className="text-xs text-[var(--muted)] leading-relaxed">
              {t("audit.clearModal.confirm")}
            </p>

            {clearError && (
              <div className="flex items-center gap-2 rounded-xl border border-[var(--danger)]/30 bg-[var(--danger)]/10 p-3 text-xs text-[var(--danger)]">
                <AlertTriangle size={15} />
                <span>{clearError}</span>
              </div>
            )}

            <div className="flex items-center justify-end gap-3 pt-3 border-t border-[var(--border)]">
              <button
                type="button"
                disabled={clearMutation.isPending}
                onClick={() => setIsClearModalOpen(false)}
                className="rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors disabled:opacity-50"
              >
                {t("audit.clearModal.cancel")}
              </button>
              <button
                type="button"
                disabled={clearMutation.isPending}
                onClick={handleConfirmClear}
                className="inline-flex items-center gap-2 rounded-xl bg-[var(--danger)] hover:bg-red-600 text-white px-5 py-2 text-xs font-medium shadow-xs transition-colors disabled:opacity-50"
              >
                {clearMutation.isPending ? (
                  <>
                    <span className="inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white border-t-transparent" />
                    <span>{t("audit.clearModal.clearing")}</span>
                  </>
                ) : (
                  <>
                    <Trash2 size={14} />
                    <span>{t("audit.clearModal.clearAction")}</span>
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
