import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import {
  AlertCircle,
  CheckCircle2,
  Globe,
  Loader2,
  Network,
  Radio,
  RotateCcw,
  Wifi,
} from "lucide-react";
import { NetworkCard } from "./NetworkCard";
import { NetworkCardSkeleton } from "./NetworkCardSkeleton";
import { NetworkEditModal } from "./NetworkEditModal";
import { NetworkMigrationModal } from "./NetworkMigrationModal";
import { WatchdogCountdownBanner } from "./WatchdogCountdownBanner";
import {
  useApplyNetworkConfigMutation,
  useConfirmNetworkMutation,
  useNetworkInterfacesQuery,
  useNetworkStatusQuery,
  useRollbackNetworkMutation,
} from "../hooks/useNetwork";
import type { ApplyResponse, InterfaceConfig, InterfaceInfo } from "../types";

export const NetworkSettingsTab: FC = () => {
  const { t } = useTranslation();
  const {
    data: interfaces = [],
    isLoading,
    isError,
    refetch,
  } = useNetworkInterfacesQuery();
  const { data: transactionState } = useNetworkStatusQuery();

  const applyMutation = useApplyNetworkConfigMutation();
  const confirmMutation = useConfirmNetworkMutation();
  const rollbackMutation = useRollbackNetworkMutation();

  const [editingIface, setEditingIface] = useState<InterfaceInfo | null>(null);
  const [migrationData, setMigrationData] = useState<ApplyResponse | null>(null);
  const [isUrlMigrationDismissed, setIsUrlMigrationDismissed] = useState(false);
  const [feedbackNotice, setFeedbackNotice] = useState<{ type: "success" | "error"; text: string } | null>(null);

  const urlToken = typeof window !== "undefined"
    ? new URLSearchParams(window.location.search).get("token") ||
      new URLSearchParams(window.location.hash.split("?")[1] || "").get("token")
    : null;

  const urlMigrationData: ApplyResponse | null = (!isUrlMigrationDismissed && urlToken && transactionState && transactionState.status === "pending_confirm")
    ? {
        transactionId: transactionState.transactionId,
        timeoutSec: transactionState.timeoutSec,
        targetUrl: transactionState.targetUrl,
        confirmToken: urlToken,
      }
    : null;

  const activeMigrationData = migrationData ?? urlMigrationData;

  function showNotice(type: "success" | "error", text: string) {
    setFeedbackNotice({ type, text });
    setTimeout(() => setFeedbackNotice(null), 5000);
  }

  async function handleApply(ifaceName: string, config: InterfaceConfig) {
    const isCurrent = editingIface?.isCurrent ?? false;
    const res = await applyMutation.mutateAsync({ iface: ifaceName, config });
    setEditingIface(null);

    if (isCurrent) {
      setMigrationData(res);
    } else {
      showNotice("success", t("systemSettings.network.watchdog.confirmSuccess"));
    }
  }

  async function handleConfirm(token: string) {
    try {
      await confirmMutation.mutateAsync(token);
      setMigrationData(null);
      setIsUrlMigrationDismissed(true);
      showNotice("success", t("systemSettings.network.watchdog.confirmSuccess"));
    } catch (err: unknown) {
      if (err instanceof Error) {
        showNotice("error", err.message);
      }
    }
  }

  async function handleRollback(token: string) {
    try {
      await rollbackMutation.mutateAsync(token);
      setMigrationData(null);
      setIsUrlMigrationDismissed(true);
      showNotice("success", t("systemSettings.network.watchdog.rollbackSuccess"));
    } catch (err: unknown) {
      if (err instanceof Error) {
        showNotice("error", err.message);
      }
    }
  }

  const totalInterfaces = interfaces.length;
  const linkUpCount = interfaces.filter((i) => i.linkUp).length;
  const currentIface = interfaces.find((i) => i.isCurrent);
  const defaultGwIface = interfaces.find((i) => i.isDefaultGw);

  return (
    <div className="flex flex-col gap-6 w-full">
      {/* Active Watchdog Trial Banner */}
      {transactionState && transactionState.status === "pending_confirm" && (
        <WatchdogCountdownBanner
          transaction={transactionState}
          onConfirm={handleConfirm}
          onRollback={handleRollback}
          isConfirming={confirmMutation.isPending}
          isRollingBack={rollbackMutation.isPending}
        />
      )}

      {/* Global Feedback Notice */}
      {feedbackNotice && (
        <div
          role="status"
          className={`flex items-center gap-2.5 rounded-xl border p-4 text-xs font-semibold animate-in fade-in ${
            feedbackNotice.type === "success"
              ? "border-[var(--positive)]/30 bg-[var(--positive-soft)] text-[var(--positive)]"
              : "border-[var(--danger)]/30 bg-[var(--danger-soft)] text-[var(--danger)]"
          }`}
        >
          {feedbackNotice.type === "success" ? (
            <CheckCircle2 size={16} aria-hidden="true" />
          ) : (
            <AlertCircle size={16} aria-hidden="true" />
          )}
          <span>{feedbackNotice.text}</span>
        </div>
      )}

      {/* KPI Metrics Strip */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 sm:gap-4">
        {/* Metric 1: Total Interfaces */}
        <div className="flex items-center gap-3 p-3.5 sm:p-4 rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-xs hover:border-[var(--border-strong,var(--accent))]/40 transition-colors">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--accent)]/10 text-[var(--accent)]">
            <Network size={20} aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="text-xs font-medium text-[var(--muted)] truncate">{t("systemSettings.network.metrics.total")}</div>
            <div className="text-xl font-bold tracking-tight text-[var(--foreground)] sm:text-2xl">{totalInterfaces}</div>
          </div>
        </div>

        {/* Metric 2: Link Up */}
        <div className="flex items-center gap-3 p-3.5 sm:p-4 rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-xs hover:border-[var(--border-strong,var(--accent))]/40 transition-colors">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--positive)]/10 text-[var(--positive)]">
            <Wifi size={20} aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="text-xs font-medium text-[var(--muted)] truncate">{t("systemSettings.network.metrics.linkUp")}</div>
            <div className="text-xl font-bold tracking-tight text-[var(--foreground)] sm:text-2xl">{linkUpCount}</div>
          </div>
        </div>

        {/* Metric 3: Current Interface */}
        <div className="flex items-center gap-3 p-3.5 sm:p-4 rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-xs hover:border-[var(--border-strong,var(--accent))]/40 transition-colors">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-soft)] text-[var(--accent)]">
            <Radio size={20} aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="text-xs font-medium text-[var(--muted)] truncate">{t("systemSettings.network.metrics.current")}</div>
            <div className="text-sm font-bold tracking-tight text-[var(--foreground)] truncate font-mono sm:text-base">
              {currentIface ? currentIface.name : "-"}
            </div>
          </div>
        </div>

        {/* Metric 4: Default Gateway */}
        <div className="flex items-center gap-3 p-3.5 sm:p-4 rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-xs hover:border-[var(--border-strong,var(--accent))]/40 transition-colors">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--surface-muted)] text-[var(--muted)] border border-[var(--border)]">
            <Globe size={20} aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <div className="text-xs font-medium text-[var(--muted)] truncate">{t("systemSettings.network.metrics.gateway")}</div>
            <div
              className="text-sm font-bold tracking-tight text-[var(--foreground)] truncate font-mono sm:text-base"
              title={defaultGwIface?.gateway || "-"}
            >
              {defaultGwIface?.gateway || "-"}
            </div>
          </div>
        </div>
      </div>

      {/* Content States */}
      {isLoading ? (
        <div className="space-y-3">
          <div className="flex items-center gap-2 text-xs text-[var(--muted)] px-1">
            <Loader2 size={13} className="animate-spin text-[var(--accent)]" aria-hidden="true" />
            <span>{t("systemSettings.network.skeletonLoading")}</span>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4" role="status" aria-label="Loading network interfaces">
            <NetworkCardSkeleton />
            <NetworkCardSkeleton />
            <NetworkCardSkeleton />
          </div>
        </div>
      ) : isError ? (
        <div className="flex min-h-[260px] flex-col items-center justify-center gap-3 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 text-center">
          <AlertCircle size={32} className="text-[var(--danger)]" aria-hidden="true" />
          <p className="text-sm font-semibold text-[var(--foreground)]">
            {t("systemSettings.network.loadError")}
          </p>
          <button
            type="button"
            onClick={() => void refetch()}
            className="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent)] px-4 py-2 text-xs font-semibold text-white hover:opacity-90 active:scale-[0.98] transition-all cursor-pointer shadow-xs"
          >
            <RotateCcw size={13} aria-hidden="true" />
            <span>{t("health.retry")}</span>
          </button>
        </div>
      ) : interfaces.length === 0 ? (
        <div
          className="flex min-h-[300px] flex-col items-center justify-center rounded-2xl border border-dashed border-[var(--border)] p-8 text-center bg-[var(--surface-muted)]/50"
          role="status"
        >
          <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--surface)] text-[var(--muted)] border border-[var(--border)] shadow-xs mb-3">
            <Network size={22} aria-hidden="true" />
          </div>
          <h3 className="text-sm font-semibold text-[var(--foreground)]">
            {t("systemSettings.network.empty")}
          </h3>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {interfaces.map((iface) => (
            <NetworkCard
              key={iface.name}
              iface={iface}
              onEdit={(target) => setEditingIface(target)}
            />
          ))}
        </div>
      )}

      {/* Edit Interface Modal */}
      {editingIface && (
        <NetworkEditModal
          key={editingIface.name}
          iface={editingIface}
          allInterfaces={interfaces}
          isOpen={true}
          onClose={() => setEditingIface(null)}
          onApply={handleApply}
          isApplying={applyMutation.isPending}
        />
      )}

      {/* Migration / Safe Reconfiguration Modal */}
      {activeMigrationData && (
        <NetworkMigrationModal
          migration={activeMigrationData}
          isOpen={true}
          onConfirm={handleConfirm}
          onRollback={handleRollback}
          onClose={() => {
            setMigrationData(null);
            setIsUrlMigrationDismissed(true);
          }}
          isConfirming={confirmMutation.isPending}
          isRollingBack={rollbackMutation.isPending}
        />
      )}
    </div>
  );
};
