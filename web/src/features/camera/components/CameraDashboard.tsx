import { AlertCircle, AlertTriangle, CheckCircle2, Video, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { calculateCameraCounts, type CameraCounts } from "../utils/statusHelper";
import type { CameraResponse } from "../types";

export interface CameraDashboardProps {
  cameras: CameraResponse[];
  counts?: CameraCounts;
}

interface MetricItem {
  key: string;
  labelKey: string;
  value: number;
  icon: LucideIcon;
  colorClass: string;
  bgClass: string;
}

export function CameraDashboard({ cameras, counts }: CameraDashboardProps) {
  const { t } = useTranslation();
  const stats = counts ?? calculateCameraCounts(cameras);

  const metrics: MetricItem[] = [
    {
      key: "total",
      labelKey: "camera.total",
      value: stats.all,
      icon: Video,
      colorClass: "text-[var(--accent)]",
      bgClass: "bg-[var(--accent)]/10",
    },
    {
      key: "online",
      labelKey: "camera.online",
      value: stats.online,
      icon: CheckCircle2,
      colorClass: "text-[var(--positive)]",
      bgClass: "bg-[var(--positive)]/10",
    },
    {
      key: "offline",
      labelKey: "camera.offline",
      value: stats.offline,
      icon: AlertCircle,
      colorClass: "text-[var(--muted)]",
      bgClass: "bg-[var(--muted)]/10",
    },
    {
      key: "abnormal",
      labelKey: "camera.abnormal",
      value: stats.abnormal,
      icon: AlertTriangle,
      colorClass: "text-[var(--danger)]",
      bgClass: "bg-[var(--danger)]/10",
    },
  ];

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 sm:gap-4 mb-6">
      {metrics.map((m) => (
        <div
          key={m.key}
          className="dashboard-metric-card flex items-center gap-3 p-3.5 sm:p-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-xs"
        >
          <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${m.bgClass} ${m.colorClass}`}>
            <m.icon size={20} aria-hidden="true" />
          </div>
          <div>
            <div className="text-xs font-medium text-[var(--muted)]">{t(m.labelKey)}</div>
            <div className={`text-xl font-semibold tracking-tight ${m.colorClass}`}>{m.value}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
