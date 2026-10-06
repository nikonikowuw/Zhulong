import { useState } from "react";
import { AlertTriangle, LoaderCircle, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { CameraResponse } from "../types";

interface DeleteConfirmModalProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: (id: string) => Promise<unknown>;
  camera: CameraResponse | null;
}

export function DeleteConfirmModal({
  isOpen,
  onClose,
  onConfirm,
  camera,
}: DeleteConfirmModalProps) {
  const { t } = useTranslation();
  const [isDeleting, setIsDeleting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  if (!isOpen || !camera) return null;

  async function handleDelete() {
    if (!camera) return;
    setErrorMsg(null);
    setIsDeleting(true);
    try {
      await onConfirm(camera.id);
      onClose();
    } catch (err: unknown) {
      if (err instanceof Error) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(String(err));
      }
    } finally {
      setIsDeleting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in duration-200"
      role="dialog"
      aria-modal="true"
      aria-labelledby="delete-modal-title"
    >
      <div className="w-full max-w-md rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl">
        <div className="flex items-center justify-between pb-3 border-b border-[var(--border)]">
          <div className="flex items-center gap-2 text-[var(--danger)]">
            <AlertTriangle size={18} />
            <h2 id="delete-modal-title" className="text-base font-semibold">
              {t("camera.deleteConfirmTitle")}
            </h2>
          </div>
          <button
            type="button"
            className="p-1 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            onClick={onClose}
            disabled={isDeleting}
          >
            <X size={18} />
          </button>
        </div>

        <div className="py-4">
          <p className="text-xs text-[var(--foreground)] leading-relaxed">
            {t("camera.deleteConfirmText", { name: camera.name })}
          </p>

          {errorMsg && (
            <div className="mt-3 text-xs text-[var(--danger)] font-medium">
              {errorMsg}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-3 pt-3 border-t border-[var(--border)]">
          <button
            type="button"
            disabled={isDeleting}
            onClick={onClose}
            className="rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
          >
            {t("camera.cancel")}
          </button>
          <button
            type="button"
            disabled={isDeleting}
            onClick={() => void handleDelete()}
            className="inline-flex items-center gap-2 rounded-xl bg-[var(--danger)] hover:bg-red-600 text-white px-5 py-2 text-xs font-medium shadow-xs transition-colors disabled:opacity-50"
          >
            {isDeleting && <LoaderCircle size={14} className="animate-spin" />}
            <span>{t("camera.delete")}</span>
          </button>
        </div>
      </div>
    </div>
  );
}
