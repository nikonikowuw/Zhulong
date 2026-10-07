import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, CheckCircle2, Loader2, Network, RefreshCw } from "lucide-react";
import { NetworkCard } from "./NetworkCard";
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
    isRefetching,
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

  return (
    <div className="flex flex-col gap-6">
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
          className={`flex items-center gap-2 rounded-xl border p-4 text-xs font-medium animate-in fade-in ${
            feedbackNotice.type === "success"
              ? "border-[var(--positive)]/30 bg-[var(--positive-soft)] text-[var(--positive)]"
              : "border-[var(--negative)]/30 bg-[var(--negative-soft)] text-[var(--negative)]"
          }`}
        >
          {feedbackNotice.type === "success" ? <CheckCircle2 size={16} /> : <AlertCircle size={16} />}
          <span>{feedbackNotice.text}</span>
        </div>
      )}

      {/* Section Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-[var(--foreground)]">
            {t("systemSettings.network.heading")}
          </h2>
          <p className="text-xs text-[var(--muted)] mt-1">
            {t("systemSettings.network.description")}
          </p>
        </div>

        <button
          type="button"
          onClick={() => refetch()}
          disabled={isLoading || isRefetching}
          className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] transition-colors hover:bg-[var(--surface-hover)] disabled:opacity-50"
        >
          <RefreshCw size={13} className={isRefetching ? "animate-spin" : ""} />
          <span>{t("health.refresh")}</span>
        </button>
      </div>

      {/* Content States */}
      {isLoading ? (
        <div className="flex h-64 flex-col items-center justify-center gap-3 rounded-2xl border border-[var(--border)] bg-[var(--surface)]">
          <Loader2 size={24} className="animate-spin text-[var(--accent)]" />
          <span className="text-xs text-[var(--muted)]">{t("health.checking")}</span>
        </div>
      ) : isError ? (
        <div className="flex h-64 flex-col items-center justify-center gap-3 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 text-center">
          <AlertCircle size={32} className="text-[var(--negative)]" />
          <p className="text-sm font-medium text-[var(--foreground)]">
            {t("systemSettings.network.loadError")}
          </p>
          <button
            type="button"
            onClick={() => refetch()}
            className="rounded-lg bg-[var(--accent)] px-4 py-2 text-xs font-medium text-white hover:opacity-90"
          >
            {t("health.retry")}
          </button>
        </div>
      ) : interfaces.length === 0 ? (
        <div className="flex h-64 flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-[var(--border)] bg-[var(--surface)] p-6 text-center">
          <Network size={32} className="text-[var(--muted)]" />
          <p className="text-sm font-medium text-[var(--muted)]">
            {t("systemSettings.network.empty")}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
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
