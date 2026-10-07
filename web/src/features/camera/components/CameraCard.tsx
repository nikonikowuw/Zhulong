import { useState } from "react";
import {
  Activity,
  Check,
  Copy,
  Edit2,
  Eye,
  EyeOff,
  Film,
  Stethoscope,
  Trash2,
  Tv,
  type LucideIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { getHealthBadgeConfig, getSessionBadgeConfig } from "../utils/statusHelper";
import { copyToClipboard, formatFps, formatResolution, maskRtspUrl } from "../utils/urlHelper";
import type { CameraResponse, StreamResponse } from "../types";

interface StreamItemProps {
  stream: StreamResponse;
  label: string;
  icon: LucideIcon;
}

function StreamItem({ stream, label, icon: Icon }: StreamItemProps) {
  const { t } = useTranslation();
  const [isRevealed, setIsRevealed] = useState(false);
  const [isCopied, setIsCopied] = useState(false);

  async function handleCopy() {
    const success = await copyToClipboard(stream.rtspUrl);
    if (success) {
      setIsCopied(true);
      setTimeout(() => setIsCopied(false), 2000);
    }
  }

  const toggleTitle = isRevealed ? t("camera.maskPassword") : t("camera.revealPassword");

  return (
    <div className="rounded-xl border border-[var(--border)]/70 bg-[var(--surface-muted)] p-3">
      <div className="flex items-center justify-between text-xs font-medium mb-1.5">
        <div className="flex items-center gap-1.5 text-[var(--foreground)]">
          <Icon
            size={14}
            className={stream.role === "main" ? "text-[var(--accent)]" : "text-[var(--muted)]"}
            aria-hidden="true"
          />
          <span>{label}</span>
          <span className="rounded bg-[var(--surface)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--muted)] border border-[var(--border)]">
            {stream.transport.toUpperCase()}
          </span>
        </div>
        <div className="font-mono text-[11px] text-[var(--muted)]">
          {formatResolution(stream.width, stream.height)} · {stream.codec} · {formatFps(stream.fps, stream.fpsString)}
        </div>
      </div>

      <div className="flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-2.5 py-1.5 text-xs font-mono">
        <span className="flex-1 truncate select-all text-[var(--muted)]">
          {isRevealed ? stream.rtspUrl : maskRtspUrl(stream.rtspUrl)}
        </span>
        <button
          type="button"
          className="p-1 rounded text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
          onClick={() => setIsRevealed((prev) => !prev)}
          title={toggleTitle}
          aria-label={toggleTitle}
        >
          {isRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
        <button
          type="button"
          className="p-1 rounded text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
          onClick={() => void handleCopy()}
          title={isCopied ? t("camera.copied") : t("camera.copyUrl")}
          aria-label={isCopied ? t("camera.copied") : t("camera.copyUrl")}
        >
          {isCopied ? (
            <Check size={14} className="text-[var(--positive)]" />
          ) : (
            <Copy size={14} />
          )}
        </button>
      </div>
    </div>
  );
}

export interface CameraCardProps {
  camera: CameraResponse;
  onEdit: (camera: CameraResponse) => void;
  onDelete: (camera: CameraResponse) => void;
  onDiagnose: (camera: CameraResponse) => void;
  onToggleEnabled: (camera: CameraResponse) => void;
  isToggling?: boolean;
}

export function CameraCard({
  camera,
  onEdit,
  onDelete,
  onDiagnose,
  onToggleEnabled,
  isToggling = false,
}: CameraCardProps) {
  const { t } = useTranslation();
  const healthConfig = getHealthBadgeConfig(camera.health);
  const sessionConfig = getSessionBadgeConfig(camera.session);

  const mainStream = camera.streams.find((s) => s.role === "main");
  const subStream = camera.streams.find((s) => s.role === "sub");

  return (
    <article
      className="camera-card flex flex-col rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 shadow-xs transition-shadow hover:shadow-md"
      aria-labelledby={`camera-title-${camera.id}`}
    >
      {/* Header: Name, Switch, Badges */}
      <div className="flex items-start justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3
              id={`camera-title-${camera.id}`}
              className="truncate text-base font-semibold text-[var(--foreground)]"
              title={camera.name}
            >
              {camera.name}
            </h3>
            <span className="font-mono text-xs text-[var(--muted)] opacity-80">
              #{camera.id.slice(-6)}
            </span>
          </div>

          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            {/* Health status badge */}
            <span className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium border border-[var(--border)] bg-[var(--surface-muted)]">
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: healthConfig.dotColor }}
                aria-hidden="true"
              />
              {t(healthConfig.labelKey)}
            </span>

            {/* Session status badge */}
            <span className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium border border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)]">
              <Activity size={12} style={{ color: sessionConfig.dotColor }} aria-hidden="true" />
              {t(sessionConfig.labelKey)}
            </span>

            {/* Degraded tag */}
            {camera.degraded && (
              <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-[var(--warning)]/15 text-amber-600 dark:text-amber-400 border border-amber-500/20">
                {t("camera.degraded")}
              </span>
            )}

            {/* Stale tag */}
            {camera.stale && (
              <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-[var(--muted)]/15 text-[var(--muted)] border border-[var(--border)]">
                {t("camera.stale")}
              </span>
            )}
          </div>
        </div>

        {/* Enable / Disable toggle switch */}
        <div className="flex items-center gap-2">
          <label className="relative inline-flex cursor-pointer items-center" title={camera.enabled ? t("camera.enabled") : t("camera.disabled")}>
            <input
              type="checkbox"
              className="sr-only peer"
              checked={camera.enabled}
              disabled={isToggling}
              onChange={() => onToggleEnabled(camera)}
              aria-label={camera.enabled ? t("camera.disable") : t("camera.enable")}
            />
            <div className="h-6 w-11 rounded-full bg-[var(--border)] peer-checked:bg-[var(--accent)] peer-focus-visible:ring-2 peer-focus-visible:ring-[var(--focus)] transition-colors after:absolute after:top-[2px] after:left-[2px] after:h-5 after:w-5 after:rounded-full after:bg-white after:shadow-xs after:transition-transform peer-checked:after:translate-x-5" />
          </label>
        </div>
      </div>

      {/* Streams list */}
      <div className="flex flex-col gap-3 py-4 flex-1">
        {mainStream && <StreamItem stream={mainStream} label={t("camera.mainStream")} icon={Tv} />}
        {subStream && <StreamItem stream={subStream} label={t("camera.subStream")} icon={Film} />}
      </div>

      {/* Card Footer Actions */}
      <div className="flex items-center justify-between border-t border-[var(--border)] pt-3 mt-auto">
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
          onClick={() => onDiagnose(camera)}
          title={t("camera.diagnose")}
        >
          <Stethoscope size={14} aria-hidden="true" />
          <span>{t("camera.diagnose")}</span>
        </button>

        <div className="flex items-center gap-1">
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs font-medium text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            onClick={() => onEdit(camera)}
            title={t("camera.edit")}
          >
            <Edit2 size={13} aria-hidden="true" />
            <span>{t("camera.edit")}</span>
          </button>
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs font-medium text-[var(--danger)] hover:bg-[var(--danger-soft)] transition-colors"
            onClick={() => onDelete(camera)}
            title={t("camera.delete")}
          >
            <Trash2 size={13} aria-hidden="true" />
            <span>{t("camera.delete")}</span>
          </button>
        </div>
      </div>
    </article>
  );
}
