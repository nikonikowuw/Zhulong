import { AlertTriangle, CheckCircle2, LoaderCircle, Stethoscope, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { CameraResponse, DiagnoseResponse } from "../types";

export interface CameraDiagnoseModalProps {
  isOpen: boolean;
  onClose: () => void;
  camera: CameraResponse | null;
  result: DiagnoseResponse | null;
  isLoading: boolean;
  onReDiagnose: () => void;
}

export function CameraDiagnoseModal({
  isOpen,
  onClose,
  camera,
  result,
  isLoading,
  onReDiagnose,
}: CameraDiagnoseModalProps) {
  const { t } = useTranslation();

  if (!isOpen || !camera) return null;

  const isSuccess = result?.state?.health === "online";
  const streams = result?.state?.streams ? Object.entries(result.state.streams) : [];

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in duration-200"
      role="dialog"
      aria-modal="true"
      aria-labelledby="diagnose-modal-title"
    >
      <div className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl">
        <div className="flex items-center justify-between pb-4 border-b border-[var(--border)]">
          <div className="flex items-center gap-2 text-[var(--foreground)]">
            <Stethoscope size={18} className="text-[var(--accent)]" />
            <h2 id="diagnose-modal-title" className="text-base font-semibold">
              {t("camera.diagnoseTitle")}
            </h2>
          </div>
          <button
            type="button"
            className="p-1 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            onClick={onClose}
          >
            <X size={18} />
          </button>
        </div>

        <div className="py-5">
          <div className="text-xs text-[var(--muted)] mb-2">
            {t("camera.name")}: <span className="font-semibold text-[var(--foreground)]">{camera.name}</span>
          </div>

          {isLoading ? (
            <div className="flex flex-col items-center justify-center py-8 gap-3">
              <LoaderCircle size={28} className="animate-spin text-[var(--accent)]" />
              <div className="text-xs text-[var(--muted)]">{t("camera.diagnosing")}</div>
            </div>
          ) : result ? (
            <div className="space-y-4">
              <div
                className={`flex items-start gap-3 rounded-xl p-3.5 border ${
                  isSuccess
                    ? "border-[var(--positive)]/30 bg-[var(--positive)]/10 text-[var(--positive)]"
                    : "border-[var(--danger)]/30 bg-[var(--danger-soft)] text-[var(--danger)]"
                }`}
              >
                {isSuccess ? (
                  <CheckCircle2 size={18} className="shrink-0 mt-0.5" />
                ) : (
                  <AlertTriangle size={18} className="shrink-0 mt-0.5" />
                )}
                <div className="text-xs">
                  <div className="font-semibold">
                    {result.message || (isSuccess ? t("camera.diagnoseSuccess") : t("camera.health.error"))}
                  </div>
                  {result.state?.reason && (
                    <div className="mt-1 opacity-90">{result.state.reason}</div>
                  )}
                </div>
              </div>

              {streams.length > 0 && (
                <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3">
                  <div className="text-xs font-semibold text-[var(--foreground)] mb-2">
                    {t("camera.probeResult")}
                  </div>
                  <div className="space-y-2">
                    {streams.map(([role, s]) => (
                      <div
                        key={role}
                        className="flex items-center justify-between text-xs py-1 border-b border-[var(--border)] last:border-b-0"
                      >
                        <span className="font-medium text-[var(--foreground)] capitalize">{role}</span>
                        <span
                          className={`font-mono text-[11px] ${
                            s.health === "online" ? "text-[var(--positive)]" : "text-[var(--danger)]"
                          }`}
                        >
                          {s.health} · {s.session}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          ) : null}
        </div>

        <div className="flex items-center justify-end gap-3 pt-3 border-t border-[var(--border)]">
          <button
            type="button"
            disabled={isLoading}
            onClick={onReDiagnose}
            className="rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors disabled:opacity-50"
          >
            {t("camera.refresh")}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="rounded-xl bg-[var(--button-background)] hover:bg-[var(--button-hover)] text-white px-5 py-2 text-xs font-medium shadow-xs transition-colors"
          >
            {t("camera.cancel")}
          </button>
        </div>
      </div>
    </div>
  );
}
