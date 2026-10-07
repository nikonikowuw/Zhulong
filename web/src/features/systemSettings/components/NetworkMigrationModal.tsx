import { useState, useEffect, type FC } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Check, Copy, ExternalLink, Loader2, RotateCcw, ShieldCheck } from "lucide-react";
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

  const progressPercentage = Math.max(0, Math.min(100, (secondsRemaining / (migration.timeoutSec || 60)) * 100));

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-md animate-in fade-in">
      <div className="relative w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl ring-1 ring-black/5 dark:ring-white/10">
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="absolute right-4 top-4 rounded-lg p-1.5 text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
        >
          <RotateCcw size={16} />
        </button>
        {/* Warning Icon & Heading */}
        <div className="flex flex-col items-center text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-[var(--warning-soft)] text-[var(--warning)] ring-8 ring-[var(--warning-soft)]/50 mb-3">
            <AlertTriangle size={28} />
          </div>
          <h2 className="text-lg font-bold text-[var(--foreground)]">
            {t("systemSettings.network.watchdog.migrationModalTitle")}
          </h2>
          <p className="mt-2 text-xs text-[var(--muted)] leading-relaxed">
            {t("systemSettings.network.watchdog.migrationWarning")}
          </p>
        </div>

        {/* Countdown Progress */}
        <div className="mt-5 rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-4">
          <div className="flex items-center justify-between text-xs font-medium mb-1.5">
            <span className="text-[var(--foreground)]">
              {t("systemSettings.network.watchdog.countdown", { seconds: secondsRemaining })}
            </span>
            <span className="font-mono text-[var(--accent)] font-semibold">{secondsRemaining}s</span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--surface)] border border-[var(--border-subtle)]">
            <div
              className={`h-full transition-all duration-1000 ${
                secondsRemaining <= 15 ? "bg-[var(--negative)]" : "bg-[var(--accent)]"
              }`}
              style={{ width: `${progressPercentage}%` }}
            />
          </div>
        </div>

        {/* Target URL Box */}
        <div className="mt-4">
          <label className="block text-xs font-medium text-[var(--muted)] mb-1">
            {t("systemSettings.network.watchdog.targetUrl")}
          </label>
          <div className="flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] p-2 text-xs font-mono">
            <span className="flex-1 truncate select-all text-[var(--foreground)] font-medium">
              {migration.targetUrl || "http://..."}
            </span>
            <button
              type="button"
              onClick={handleCopy}
              className="p-1 rounded text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface)] transition-colors"
              title={t("systemSettings.network.actions.copyUrl")}
              aria-label={t("systemSettings.network.actions.copyUrl")}
            >
              {copiedUrl ? <Check size={14} className="text-[var(--positive)]" /> : <Copy size={14} />}
            </button>
            <button
              type="button"
              onClick={handleJump}
              className="p-1 rounded text-[var(--accent)] hover:bg-[var(--surface)] transition-colors"
              title={t("systemSettings.network.watchdog.jumpNow")}
            >
              <ExternalLink size={14} />
            </button>
          </div>
        </div>

        {/* Actions */}
        <div className="mt-6 flex flex-col gap-2.5">
          <button
            type="button"
            onClick={handleJump}
            className="flex w-full items-center justify-center gap-2 rounded-xl bg-[var(--accent)] py-2.5 text-xs font-semibold text-white shadow-sm hover:opacity-90 transition-opacity"
          >
            <ExternalLink size={14} />
            {t("systemSettings.network.watchdog.jumpNow")}
          </button>

          <div className="grid grid-cols-2 gap-2">
            <button
              type="button"
              onClick={() => onConfirm(migration.confirmToken)}
              disabled={isConfirming || isRollingBack || secondsRemaining <= 0}
              className="flex items-center justify-center gap-1.5 rounded-xl border border-[var(--positive)]/30 bg-[var(--positive-soft)] py-2 text-xs font-medium text-[var(--positive)] hover:bg-[var(--positive)]/20 disabled:opacity-50 transition-colors"
            >
              {isConfirming ? <Loader2 size={13} className="animate-spin" /> : <ShieldCheck size={14} />}
              {t("systemSettings.network.actions.confirm")}
            </button>
            <button
              type="button"
              onClick={() => onRollback(migration.confirmToken)}
              disabled={isConfirming || isRollingBack}
              className="flex items-center justify-center gap-1.5 rounded-xl border border-[var(--negative)]/30 bg-[var(--negative-soft)] py-2 text-xs font-medium text-[var(--negative)] hover:bg-[var(--negative)]/20 disabled:opacity-50 transition-colors"
            >
              {isRollingBack ? <Loader2 size={13} className="animate-spin" /> : <RotateCcw size={14} />}
              {t("systemSettings.network.actions.rollback")}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
