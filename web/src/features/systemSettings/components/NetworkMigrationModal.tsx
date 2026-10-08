import { useState, useEffect, type FC } from "react";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  Check,
  Copy,
  ExternalLink,
  Loader2,
  RotateCcw,
  ShieldCheck,
  X,
} from "lucide-react";
import type { ApplyResponse } from "../types";

interface NetworkMigrationModalProps {
  migration: ApplyResponse;
  isOpen: boolean;
  onConfirm: (token: string) => Promise<void>;
  onRollback: (token: string) => Promise<void>;
  onClose: () => void;
  isConfirming: boolean;
  isRollingBack: boolean;
}

export const NetworkMigrationModal: FC<NetworkMigrationModalProps> = ({
  migration,
  isOpen,
  onConfirm,
  onRollback,
  onClose,
  isConfirming,
  isRollingBack,
}) => {
  const { t } = useTranslation();
  const [secondsRemaining, setSecondsRemaining] = useState(migration.timeoutSec || 60);
  const [copiedUrl, setCopiedUrl] = useState(false);

  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  useEffect(() => {
    if (!isOpen || secondsRemaining <= 0) return;
    const interval = setInterval(() => {
      setSecondsRemaining((prev) => {
        if (prev <= 1) {
          clearInterval(interval);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    return () => clearInterval(interval);
  }, [isOpen, secondsRemaining]);

  if (!isOpen) return null;

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(migration.targetUrl);
      setCopiedUrl(true);
      setTimeout(() => setCopiedUrl(false), 2000);
    } catch {
      // clipboard ignored
    }
  }

  function handleJump() {
    if (migration.targetUrl) {
      window.location.href = migration.targetUrl;
    }
  }

  const isUrgent = secondsRemaining <= 15;
  const progressPercentage = Math.max(0, Math.min(100, (secondsRemaining / (migration.timeoutSec || 60)) * 100));

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-md animate-in fade-in"
      role="dialog"
      aria-modal="true"
      aria-labelledby="migration-modal-title"
    >
      <div className="relative w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-2xl ring-1 ring-black/5 dark:ring-white/10">
        <button
          type="button"
          onClick={onClose}
          aria-label={t("systemSettings.network.actions.cancel")}
          className="absolute right-4 top-4 rounded-lg p-1.5 text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors cursor-pointer"
        >
          <X size={18} aria-hidden="true" />
        </button>

        {/* Warning Icon & Heading */}
        <div className="flex flex-col items-center text-center">
          <div
            className={`flex h-14 w-14 items-center justify-center rounded-2xl mb-3 transition-colors ${
              isUrgent
                ? "bg-[var(--danger-soft)] text-[var(--danger)] ring-8 ring-[var(--danger-soft)]/50"
                : "bg-[var(--warning-soft)] text-[var(--warning)] ring-8 ring-[var(--warning-soft)]/50"
            }`}
          >
            <AlertTriangle size={28} aria-hidden="true" className={isUrgent ? "animate-bounce" : undefined} />
          </div>
          <h2 id="migration-modal-title" className="text-lg font-bold tracking-tight text-[var(--foreground)]">
            {t("systemSettings.network.watchdog.migrationModalTitle")}
          </h2>
          <p className="mt-2 text-xs text-[var(--muted)] leading-relaxed max-w-sm">
            {t("systemSettings.network.watchdog.migrationWarning")}
          </p>
        </div>

        {/* Countdown Progress */}
        <div
          className={`mt-5 rounded-2xl border p-4 transition-all ${
            isUrgent
              ? "border-[var(--danger)]/30 bg-[var(--danger-soft)]/40"
              : "border-[var(--border)] bg-[var(--surface-muted)]/80"
          }`}
        >
          <div className="flex items-center justify-between text-xs font-semibold mb-2">
            <span className={isUrgent ? "text-[var(--danger)] font-bold" : "text-[var(--foreground)]"}>
              {t("systemSettings.network.watchdog.countdown", { seconds: secondsRemaining })}
            </span>
            <span
              className={`font-mono font-bold text-sm ${
                isUrgent ? "text-[var(--danger)] animate-pulse" : "text-[var(--accent)]"
              }`}
            >
              {secondsRemaining}s
            </span>
          </div>
          <div className="h-2.5 w-full overflow-hidden rounded-full bg-[var(--surface)] border border-[var(--border-subtle)]">
            <div
              className={`h-full transition-all duration-1000 ${
                isUrgent ? "bg-[var(--danger)] animate-pulse" : "bg-[var(--accent)]"
              }`}
              style={{ width: `${progressPercentage}%` }}
            />
          </div>
        </div>

        {/* Target URL Box */}
        <div className="mt-4">
          <label className="block text-xs font-semibold text-[var(--foreground)] mb-1.5">
            {t("systemSettings.network.watchdog.targetUrl")}
          </label>
          <div className="flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-2 text-xs font-mono">
            <span className="flex-1 truncate select-all text-[var(--foreground)] font-semibold">
              {migration.targetUrl || "http://..."}
            </span>
            <button
              type="button"
              onClick={handleCopy}
              className="p-1.5 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface)] transition-colors cursor-pointer"
              title={t("systemSettings.network.actions.copyUrl")}
              aria-label={t("systemSettings.network.actions.copyUrl")}
            >
              {copiedUrl ? (
                <Check size={14} className="text-[var(--positive)]" />
              ) : (
                <Copy size={14} aria-hidden="true" />
              )}
            </button>
            <button
              type="button"
              onClick={handleJump}
              className="p-1.5 rounded-lg text-[var(--accent)] hover:bg-[var(--surface)] transition-colors cursor-pointer"
              title={t("systemSettings.network.watchdog.jumpNow")}
              aria-label={t("systemSettings.network.watchdog.jumpNow")}
            >
              <ExternalLink size={14} aria-hidden="true" />
            </button>
          </div>
        </div>

        {/* Actions */}
        <div className="mt-6 flex flex-col gap-2.5">
          <button
            type="button"
            onClick={handleJump}
            className="flex w-full items-center justify-center gap-2 rounded-xl bg-[var(--accent)] py-2.5 text-xs font-bold text-white shadow-sm hover:opacity-90 active:scale-[0.98] transition-all cursor-pointer"
          >
            <ExternalLink size={14} aria-hidden="true" />
            <span>{t("systemSettings.network.watchdog.jumpNow")}</span>
          </button>

          <div className="grid grid-cols-2 gap-2.5">
            <button
              type="button"
              onClick={() => onConfirm(migration.confirmToken)}
              disabled={isConfirming || isRollingBack || secondsRemaining <= 0}
              className="flex items-center justify-center gap-1.5 rounded-xl border border-[var(--positive)]/30 bg-[var(--positive-soft)] py-2.5 text-xs font-semibold text-[var(--positive)] hover:bg-[var(--positive)]/20 active:scale-[0.98] disabled:opacity-50 transition-all cursor-pointer"
            >
              {isConfirming ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <ShieldCheck size={14} aria-hidden="true" />}
              <span>{t("systemSettings.network.actions.confirm")}</span>
            </button>
            <button
              type="button"
              onClick={() => onRollback(migration.confirmToken)}
              disabled={isConfirming || isRollingBack}
              className="flex items-center justify-center gap-1.5 rounded-xl border border-[var(--danger)]/30 bg-[var(--danger-soft)] py-2.5 text-xs font-semibold text-[var(--danger)] hover:bg-[var(--danger)]/20 active:scale-[0.98] disabled:opacity-50 transition-all cursor-pointer"
            >
              {isRollingBack ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <RotateCcw size={14} aria-hidden="true" />}
              <span>{t("systemSettings.network.actions.rollback")}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
