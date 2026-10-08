import { useState, useEffect, type FC } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CheckCircle, Clock, Loader2, RotateCcw } from "lucide-react";
import type { TransactionState } from "../types";

interface WatchdogCountdownBannerProps {
  transaction: NonNullable<TransactionState>;
  onConfirm: (token: string) => Promise<void>;
  onRollback: (token: string) => Promise<void>;
  isConfirming: boolean;
  isRollingBack: boolean;
}

export const WatchdogCountdownBanner: FC<WatchdogCountdownBannerProps> = ({
  transaction,
  onConfirm,
  onRollback,
  isConfirming,
  isRollingBack,
}) => {
  const { t } = useTranslation();
  const [secondsLeft, setSecondsLeft] = useState<number>(() => {
    if (transaction.expiresAt) {
      const diff = Math.max(0, Math.floor((new Date(transaction.expiresAt).getTime() - Date.now()) / 1000));
      return diff;
    }
    return transaction.timeoutSec || 60;
  });

  useEffect(() => {
    const calculateSeconds = () => {
      if (transaction.expiresAt) {
        return Math.max(0, Math.floor((new Date(transaction.expiresAt).getTime() - Date.now()) / 1000));
      }
      return transaction.timeoutSec || 60;
    };

    const interval = setInterval(() => {
      const remaining = calculateSeconds();
      setSecondsLeft(remaining);
      if (remaining <= 0) {
        clearInterval(interval);
      }
    }, 1000);

    return () => clearInterval(interval);
  }, [transaction.expiresAt, transaction.timeoutSec]);

  if (secondsLeft <= 0) return null;

  const isCritical = secondsLeft <= 15;

  return (
    <aside
      role="alert"
      aria-live="polite"
      aria-label={t("systemSettings.network.watchdog.bannerTitle")}
      className={`sticky top-0 z-40 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 rounded-2xl border px-4 py-3 text-xs shadow-md backdrop-blur-md transition-all duration-300 animate-in slide-in-from-top ${
        isCritical
          ? "border-[var(--danger)]/50 bg-[var(--danger-soft)] text-[var(--danger)] ring-2 ring-[var(--danger)]/20"
          : "border-[var(--warning)]/40 bg-[var(--warning-soft)] text-[var(--foreground)] ring-1 ring-[var(--warning)]/20"
      }`}
    >
      <div className="flex items-center gap-3">
        <div
          className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl ${
            isCritical
              ? "bg-[var(--danger)] text-white animate-bounce"
              : "bg-[var(--warning)] text-black"
          }`}
        >
          <AlertTriangle size={18} aria-hidden="true" />
        </div>
        <div>
          <div className="flex items-center gap-2">
            <span className="font-bold tracking-tight text-[var(--foreground)]">
              {t("systemSettings.network.watchdog.bannerTitle")}
            </span>
            <span
              className={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 font-mono text-[11px] font-bold ${
                isCritical
                  ? "bg-[var(--danger)] text-white animate-pulse"
                  : "bg-[var(--surface)] text-[var(--foreground)] border border-[var(--border)]"
              }`}
            >
              <Clock size={11} aria-hidden="true" />
              <span>{secondsLeft}s</span>
            </span>
          </div>
          <p className="text-xs text-[var(--muted)] mt-0.5">
            {t("systemSettings.network.watchdog.bannerMessage", {
              iface: transaction.interfaceName,
              seconds: secondsLeft,
            })}
          </p>
        </div>
      </div>

      <div className="flex items-center gap-2 self-end sm:self-center shrink-0">
        <button
          type="button"
          onClick={() => onConfirm(transaction.confirmToken)}
          disabled={isConfirming || isRollingBack}
          className="inline-flex items-center gap-1.5 rounded-xl bg-[var(--positive)] px-3.5 py-1.5 text-xs font-semibold text-white shadow-xs hover:opacity-90 active:scale-[0.98] disabled:opacity-50 transition-all cursor-pointer"
        >
          {isConfirming ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <CheckCircle size={13} aria-hidden="true" />}
          <span>{t("systemSettings.network.actions.confirm")}</span>
        </button>
        <button
          type="button"
          onClick={() => onRollback(transaction.confirmToken)}
          disabled={isConfirming || isRollingBack}
          className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3.5 py-1.5 text-xs font-semibold text-[var(--foreground)] hover:bg-[var(--surface-hover)] hover:border-[var(--danger)] hover:text-[var(--danger)] active:scale-[0.98] disabled:opacity-50 transition-all cursor-pointer shadow-2xs"
        >
          {isRollingBack ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <RotateCcw size={13} aria-hidden="true" />}
          <span>{t("systemSettings.network.actions.rollback")}</span>
        </button>
      </div>
    </aside>
  );
};
