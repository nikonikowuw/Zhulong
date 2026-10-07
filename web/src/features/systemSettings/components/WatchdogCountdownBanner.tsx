import { useState, useEffect, type FC } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, CheckCircle, Loader2, RotateCcw } from "lucide-react";
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

  return (
    <aside
      aria-label={t("systemSettings.network.watchdog.bannerTitle")}
      className="sticky top-0 z-40 flex items-center justify-between border-b border-[var(--warning)]/30 bg-[var(--warning-soft)] px-4 py-2.5 text-xs text-[var(--foreground)] shadow-sm backdrop-blur-sm animate-in slide-in-from-top"
    >
      <div className="flex items-center gap-2.5">
        <AlertCircle size={16} className="text-[var(--warning)] shrink-0 animate-pulse" />
        <div>
          <span className="font-semibold text-[var(--warning-strong,var(--warning))] mr-1">
            {t("systemSettings.network.watchdog.bannerTitle")}:
          </span>
          <span>
            {t("systemSettings.network.watchdog.bannerMessage", {
              iface: transaction.interfaceName,
              seconds: secondsLeft,
            })}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => onConfirm(transaction.confirmToken)}
          disabled={isConfirming || isRollingBack}
          className="inline-flex items-center gap-1 rounded-lg bg-[var(--accent)] px-3 py-1 text-xs font-medium text-white shadow-sm hover:opacity-90 disabled:opacity-50 transition-opacity"
        >
          {isConfirming ? <Loader2 size={12} className="animate-spin" /> : <CheckCircle size={12} />}
          {t("systemSettings.network.actions.confirm")}
        </button>
        <button
          type="button"
          onClick={() => onRollback(transaction.confirmToken)}
          disabled={isConfirming || isRollingBack}
          className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] disabled:opacity-50 transition-colors"
        >
          {isRollingBack ? <Loader2 size={12} className="animate-spin" /> : <RotateCcw size={12} />}
          {t("systemSettings.network.actions.rollback")}
        </button>
      </div>
    </aside>
  );
};
