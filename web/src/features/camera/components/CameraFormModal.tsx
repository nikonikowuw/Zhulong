import { useState } from "react";
import { AlertCircle, LoaderCircle, Plus, Tv, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ApiError } from "@/shared/api/client";
import type { CameraResponse, CreateCameraInput, UpdateCameraInput } from "../types";

interface CameraFormModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmitCreate: (input: CreateCameraInput) => Promise<unknown>;
  onSubmitUpdate: (id: string, input: UpdateCameraInput) => Promise<unknown>;
  editingCamera: CameraResponse | null;
}

interface CameraFormDialogProps {
  onClose: () => void;
  onSubmitCreate: (input: CreateCameraInput) => Promise<unknown>;
  onSubmitUpdate: (id: string, input: UpdateCameraInput) => Promise<unknown>;
  editingCamera: CameraResponse | null;
}

function CameraFormDialog({
  onClose,
  onSubmitCreate,
  onSubmitUpdate,
  editingCamera,
}: CameraFormDialogProps) {
  const { t } = useTranslation();
  const isEdit = Boolean(editingCamera);

  const initialMain = editingCamera?.streams.find((s) => s.role === "main");
  const initialSub = editingCamera?.streams.find((s) => s.role === "sub");

  const [name, setName] = useState(editingCamera?.name ?? "");
  const [enabled, setEnabled] = useState(editingCamera?.enabled ?? true);
  const [mainUrl, setMainUrl] = useState(initialMain?.rtspUrl ?? "");
  const [mainTransport, setMainTransport] = useState<"tcp" | "udp">(initialMain?.transport ?? "tcp");
  const [enableSub, setEnableSub] = useState(Boolean(initialSub));
  const [subUrl, setSubUrl] = useState(initialSub?.rtspUrl ?? "");
  const [subTransport, setSubTransport] = useState<"tcp" | "udp">(initialSub?.transport ?? "tcp");

  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) {
      setErrorMsg(t("camera.nameRequired"));
      return;
    }
    if (!mainUrl.trim()) {
      setErrorMsg(t("camera.mainUrlRequired"));
      return;
    }

    setErrorMsg(null);
    setIsSubmitting(true);

    try {
      if (isEdit && editingCamera) {
        const updatePayload: UpdateCameraInput = {
          revision: editingCamera.revision,
          name: name.trim(),
          enabled,
          mainStream: {
            role: "main",
            rtspUrl: mainUrl.trim(),
            transport: mainTransport,
          },
          subStream: enableSub && subUrl.trim()
            ? {
                role: "sub",
                rtspUrl: subUrl.trim(),
                transport: subTransport,
              }
            : undefined,
        };
        await onSubmitUpdate(editingCamera.id, updatePayload);
      } else {
        const createPayload: CreateCameraInput = {
          name: name.trim(),
          enabled,
          mainStream: {
            role: "main",
            rtspUrl: mainUrl.trim(),
            transport: mainTransport,
          },
          subStream: enableSub && subUrl.trim()
            ? {
                role: "sub",
                rtspUrl: subUrl.trim(),
                transport: subTransport,
              }
            : undefined,
        };
        await onSubmitCreate(createPayload);
      }
      onClose();
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        if (err.status === 409) {
          setErrorMsg(t("camera.conflictWarning"));
        } else {
          setErrorMsg(err.message);
        }
      } else if (err instanceof Error) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(String(err));
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in duration-200"
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
    >
      <div className="w-full max-w-lg rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between pb-4 border-b border-[var(--border)]">
          <h2 id="modal-title" className="text-lg font-semibold text-[var(--foreground)]">
            {isEdit ? t("camera.editTitle") : t("camera.createTitle")}
          </h2>
          <button
            type="button"
            className="p-1 rounded-lg text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            onClick={onClose}
            disabled={isSubmitting}
            aria-label={t("camera.cancel")}
          >
            <X size={18} />
          </button>
        </div>

        {errorMsg && (
          <div className="mt-4 flex items-start gap-2.5 rounded-xl border border-[var(--danger)]/30 bg-[var(--danger-soft)] p-3 text-xs text-[var(--danger)]" role="alert">
            <AlertCircle size={16} className="shrink-0 mt-0.5" />
            <div className="flex-1 font-medium leading-relaxed">{errorMsg}</div>
          </div>
        )}

        {isSubmitting && (
          <div className="mt-4 flex items-center gap-2.5 rounded-xl border border-[var(--accent)]/30 bg-[var(--accent)]/10 p-3 text-xs text-[var(--accent)]" role="status">
            <LoaderCircle size={16} className="animate-spin shrink-0" />
            <div className="flex-1 font-medium">{t("camera.probingNotice")}</div>
          </div>
        )}

        <form onSubmit={handleSubmit} className="mt-5 space-y-4">
          {/* Camera Name */}
          <div>
            <label htmlFor="camera-name" className="block text-xs font-medium text-[var(--foreground)] mb-1">
              {t("camera.name")} <span className="text-[var(--danger)]">*</span>
            </label>
            <input
              id="camera-name"
              type="text"
              required
              disabled={isSubmitting}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("camera.namePlaceholder")}
              className="w-full rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] px-3.5 py-2.5 text-sm text-[var(--foreground)] focus:border-[var(--focus)] focus:bg-[var(--surface)] focus:outline-none transition-colors"
            />
          </div>

          {/* Enabled switch */}
          <div className="flex items-center justify-between py-1">
            <span className="text-xs font-medium text-[var(--foreground)]">{t("camera.enabled")}</span>
            <label className="relative inline-flex cursor-pointer items-center">
              <input
                type="checkbox"
                className="sr-only peer"
                checked={enabled}
                disabled={isSubmitting}
                onChange={(e) => setEnabled(e.target.checked)}
              />
              <div className="h-6 w-11 rounded-full bg-[var(--border)] peer-checked:bg-[var(--accent)] transition-colors after:absolute after:top-[2px] after:left-[2px] after:h-5 after:w-5 after:rounded-full after:bg-white after:transition-transform peer-checked:after:translate-x-5" />
            </label>
          </div>

          {/* Main Stream section */}
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)]/50 p-4 space-y-3">
            <div className="flex items-center gap-2 text-xs font-semibold text-[var(--foreground)]">
              <Tv size={14} className="text-[var(--accent)]" />
              <span>{t("camera.mainStream")}</span>
              <span className="text-[var(--danger)]">*</span>
            </div>

            <div>
              <label htmlFor="main-url" className="block text-xs text-[var(--muted)] mb-1">
                {t("camera.url")}
              </label>
              <input
                id="main-url"
                type="text"
                required
                disabled={isSubmitting}
                value={mainUrl}
                onChange={(e) => setMainUrl(e.target.value)}
                placeholder={t("camera.mainUrlPlaceholder")}
                className="w-full font-mono rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-xs text-[var(--foreground)] focus:border-[var(--focus)] focus:outline-none"
              />
            </div>

            <div>
              <label htmlFor="main-transport" className="block text-xs text-[var(--muted)] mb-1">
                {t("camera.transport")}
              </label>
              <select
                id="main-transport"
                disabled={isSubmitting}
                value={mainTransport}
                onChange={(e) => setMainTransport(e.target.value as "tcp" | "udp")}
                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--foreground)] focus:border-[var(--focus)] focus:outline-none"
              >
                <option value="tcp">TCP (Recommended)</option>
                <option value="udp">UDP</option>
              </select>
            </div>
          </div>

          {/* Sub Stream section */}
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-muted)]/50 p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-xs font-semibold text-[var(--foreground)]">
                <span>{t("camera.subStream")}</span>
                <span className="text-[10px] text-[var(--muted)] font-normal">(Optional)</span>
              </div>
              <label className="relative inline-flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  className="sr-only peer"
                  checked={enableSub}
                  disabled={isSubmitting}
                  onChange={(e) => setEnableSub(e.target.checked)}
                />
                <div className="h-5 w-9 rounded-full bg-[var(--border)] peer-checked:bg-[var(--accent)] transition-colors after:absolute after:top-[2px] after:left-[2px] after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-transform peer-checked:after:translate-x-4" />
              </label>
            </div>

            {enableSub && (
              <>
                <div>
                  <label htmlFor="sub-url" className="block text-xs text-[var(--muted)] mb-1">
                    {t("camera.url")}
                  </label>
                  <input
                    id="sub-url"
                    type="text"
                    disabled={isSubmitting}
                    value={subUrl}
                    onChange={(e) => setSubUrl(e.target.value)}
                    placeholder={t("camera.subUrlPlaceholder")}
                    className="w-full font-mono rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-xs text-[var(--foreground)] focus:border-[var(--focus)] focus:outline-none"
                  />
                </div>

                <div>
                  <label htmlFor="sub-transport" className="block text-xs text-[var(--muted)] mb-1">
                    {t("camera.transport")}
                  </label>
                  <select
                    id="sub-transport"
                    disabled={isSubmitting}
                    value={subTransport}
                    onChange={(e) => setSubTransport(e.target.value as "tcp" | "udp")}
                    className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs text-[var(--foreground)] focus:border-[var(--focus)] focus:outline-none"
                  >
                    <option value="tcp">TCP (Recommended)</option>
                    <option value="udp">UDP</option>
                  </select>
                </div>
              </>
            )}
          </div>

          {/* Action buttons */}
          <div className="flex items-center justify-end gap-3 pt-3 border-t border-[var(--border)]">
            <button
              type="button"
              disabled={isSubmitting}
              onClick={onClose}
              className="rounded-xl border border-[var(--border)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            >
              {t("camera.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-xl bg-[var(--button-background)] hover:bg-[var(--button-hover)] text-white px-5 py-2 text-xs font-medium shadow-xs transition-colors disabled:opacity-50"
            >
              {isSubmitting ? (
                <>
                  <LoaderCircle size={14} className="animate-spin" />
                  <span>{t("camera.probingNotice")}</span>
                </>
              ) : isEdit ? (
                t("camera.submitEdit")
              ) : (
                <>
                  <Plus size={14} />
                  <span>{t("camera.submitCreate")}</span>
                </>
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export function CameraFormModal({
  isOpen,
  onClose,
  onSubmitCreate,
  onSubmitUpdate,
  editingCamera,
}: CameraFormModalProps) {
  if (!isOpen) return null;

  return (
    <CameraFormDialog
      key={editingCamera ? `edit-${editingCamera.id}-${editingCamera.revision}` : "create"}
      onClose={onClose}
      onSubmitCreate={onSubmitCreate}
      onSubmitUpdate={onSubmitUpdate}
      editingCamera={editingCamera}
    />
  );
}
