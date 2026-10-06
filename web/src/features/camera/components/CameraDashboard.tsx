import { AlertCircle, AlertTriangle, CheckCircle2, Video } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { CameraResponse } from "../types";

interface CameraDashboardProps {
  cameras: CameraResponse[];
}

export function CameraDashboard({ cameras }: CameraDashboardProps) {
  const { t } = useTranslation();

  const total = cameras.length;
  const online = cameras.filter((c) => c.enabled && c.health === "online").length;
  const offline = cameras.filter((c) => c.enabled && c.health === "offline").length;
  const abnormal = cameras.filter(
    (c) => c.enabled && (c.health === "error" || c.degraded || c.stale),
  ).length;

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 sm:gap-4 mb-6">
      <div className="dashboard-metric-card flex items-center gap-3 p-3.5 sm:p-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-xs">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--accent)]/10 text-[var(--accent)]">
          <Video size={20} aria-hidden="true" />
        </div>
        <div>
          <div className="text-xs font-medium text-[var(--muted)]">{t("camera.total")}</div>
          <div className="text-xl font-semibold tracking-tight">{total}</div>
        </div>
      </div>

      <div className="dashboard-metric-card flex items-center gap-3 p-3.5 sm:p-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-xs">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--positive)]/10 text-[var(--positive)]">
          <CheckCircle2 size={20} aria-hidden="true" />
        </div>
        <div>
          <div className="text-xs font-medium text-[var(--muted)]">{t("camera.online")}</div>
          <div className="text-xl font-semibold tracking-tight text-[var(--positive)]">{online}</div>
        </div>
      </div>

      <div className="dashboard-metric-card flex items-center gap-3 p-3.5 sm:p-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-xs">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--muted)]/10 text-[var(--muted)]">
          <AlertCircle size={20} aria-hidden="true" />
        </div>
        <div>
          <div className="text-xs font-medium text-[var(--muted)]">{t("camera.offline")}</div>
          <div className="text-xl font-semibold tracking-tight text-[var(--muted)]">{offline}</div>
        </div>
      </div>

      <div className="dashboard-metric-card flex items-center gap-3 p-3.5 sm:p-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-xs">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--danger)]/10 text-[var(--danger)]">
          <AlertTriangle size={20} aria-hidden="true" />
        </div>
        <div>
          <div className="text-xs font-medium text-[var(--muted)]">{t("camera.abnormal")}</div>
          <div className="text-xl font-semibold tracking-tight text-[var(--danger)]">{abnormal}</div>
        </div>
      </div>
    </div>
  );
}
