import {
  ArrowRight,
  Camera,
  Cpu,
  LayoutGrid,
  ShieldCheck,
  Video,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCamerasQuery } from "@/features/camera";
import { useHealth } from "../hooks/useHealth";
import { HealthPanel } from "./HealthPanel";
import type { FC } from "react";

interface OverviewDashboardProps {
  onNavigateTab?: (tab: "overview" | "live" | "cameras") => void;
}

export const OverviewDashboard: FC<OverviewDashboardProps> = ({ onNavigateTab }) => {
  const { t } = useTranslation();
  const { data: cameras } = useCamerasQuery();
  const { data: health } = useHealth();

  const totalCameras = cameras?.length ?? 0;
  const onlineCameras = cameras?.filter((c) => c.enabled && c.health === "online").length ?? 0;
  const offlineCameras = cameras?.filter((c) => c.enabled && c.health === "offline").length ?? 0;

  const isEngineReady = health?.components.engine === "ready";
  const isDbReady = health?.components.database === "ready";

  return (
    <div className="flex flex-col gap-6 w-full animate-in fade-in duration-200">
      {/* Page Header */}
      <section className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-[var(--border)] pb-4" aria-labelledby="page-title">
        <div>
          <h1 id="page-title" className="text-xl font-bold tracking-tight text-[var(--foreground)]">
            {t("overview.heading")}
          </h1>
          <p className="text-xs text-[var(--muted)] mt-1">
            {t("overview.description")}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center gap-1.5 rounded-full bg-[var(--positive)]/10 px-2.5 py-1 text-xs font-medium text-[var(--positive)]">
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--positive)] animate-pulse" />
            {health?.status === "ready" ? t("console.dashboard.statusReady") : t("console.dashboard.statusChecking")}
          </span>
        </div>
      </section>

      {/* KPI Metrics Strip */}
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4" aria-label={t("console.dashboard.metricsTitle")}>
        {/* Metric 1: Cameras */}
        <div
          className="group relative flex flex-col justify-between rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs transition-all hover:border-[var(--accent)]/50 cursor-pointer"
          onClick={() => onNavigateTab?.("cameras")}
        >
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
              {t("console.dashboard.camerasCard")}
            </span>
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--accent)]/10 text-[var(--accent)]">
              <Camera size={18} aria-hidden="true" />
            </div>
          </div>
          <div className="mt-3">
            <div className="text-2xl font-bold tracking-tight text-[var(--foreground)]">
              {totalCameras}
            </div>
            <div className="mt-1 flex items-center justify-between text-xs text-[var(--muted)]">
              <span>{t("console.dashboard.camerasDesc", { online: onlineCameras, total: totalCameras })} {offlineCameras > 0 ? `(${offlineCameras} 离线)` : ""}</span>
              <span className="text-[var(--accent)] group-hover:translate-x-0.5 transition-transform">
                <ArrowRight size={13} aria-hidden="true" />
              </span>
            </div>
          </div>
        </div>

        {/* Metric 2: Live Video Channels */}
        <div
          className="group relative flex flex-col justify-between rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs transition-all hover:border-[var(--positive)]/50 cursor-pointer"
          onClick={() => onNavigateTab?.("live")}
        >
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
              {t("console.dashboard.liveCard")}
            </span>
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--positive)]/10 text-[var(--positive)]">
              <LayoutGrid size={18} aria-hidden="true" />
            </div>
          </div>
          <div className="mt-3">
            <div className="text-2xl font-bold tracking-tight text-[var(--positive)]">
              {onlineCameras > 0 ? `${onlineCameras}` : "0"}
            </div>
            <div className="mt-1 flex items-center justify-between text-xs text-[var(--muted)]">
              <span>{t("console.dashboard.liveDesc")}</span>
              <span className="text-[var(--positive)] group-hover:translate-x-0.5 transition-transform">
                <ArrowRight size={13} aria-hidden="true" />
              </span>
            </div>
          </div>
        </div>

        {/* Metric 3: Inference Engine */}
        <div className="flex flex-col justify-between rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
              {t("console.dashboard.engineCard")}
            </span>
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-purple-500/10 text-purple-600 dark:text-purple-400">
              <Cpu size={18} aria-hidden="true" />
            </div>
          </div>
          <div className="mt-3">
            <div className="text-base font-bold tracking-tight text-[var(--foreground)]">
              {isEngineReady ? t("console.dashboard.statusReady") : t("console.dashboard.statusChecking")}
            </div>
            <div className="mt-1 text-xs text-[var(--muted)] truncate">
              {t("console.dashboard.engineDesc")}
            </div>
          </div>
        </div>

        {/* Metric 4: System Host */}
        <div className="flex flex-col justify-between rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
              {t("console.dashboard.systemCard")}
            </span>
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
              <ShieldCheck size={18} aria-hidden="true" />
            </div>
          </div>
          <div className="mt-3">
            <div className="text-base font-bold tracking-tight text-[var(--foreground)]">
              {isDbReady ? t("console.dashboard.statusReady") : t("console.dashboard.statusChecking")}
            </div>
            <div className="mt-1 text-xs text-[var(--muted)] truncate">
              {t("console.dashboard.systemDesc")}
            </div>
          </div>
        </div>
      </section>

      {/* Main Content Grid: Health Detail & Quick Operations */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Left: Component Health Status */}
        <div className="lg:col-span-2">
          <HealthPanel />
        </div>

        {/* Right: Quick Actions & Runtime Environment */}
        <div className="flex flex-col gap-6">
          {/* Quick Operations Card */}
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs">
            <h3 className="text-sm font-semibold tracking-tight text-[var(--foreground)] mb-3">
              {t("console.dashboard.quickActions")}
            </h3>
            <div className="space-y-2">
              <button
                type="button"
                className="flex w-full items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] px-3 py-2.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
                onClick={() => onNavigateTab?.("live")}
              >
                <div className="flex items-center gap-2.5">
                  <Video size={16} className="text-[var(--accent)]" aria-hidden="true" />
                  <span>{t("console.dashboard.gotoLive")}</span>
                </div>
                <ArrowRight size={14} className="text-[var(--muted)]" aria-hidden="true" />
              </button>

              <button
                type="button"
                className="flex w-full items-center justify-between rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] px-3 py-2.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
                onClick={() => onNavigateTab?.("cameras")}
              >
                <div className="flex items-center gap-2.5">
                  <Camera size={16} className="text-[var(--positive)]" aria-hidden="true" />
                  <span>{t("console.dashboard.gotoCameras")}</span>
                </div>
                <ArrowRight size={14} className="text-[var(--muted)]" aria-hidden="true" />
              </button>
            </div>
          </div>

          {/* Environment Info Card */}
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xs">
            <h3 className="text-sm font-semibold tracking-tight text-[var(--foreground)] mb-3">
              {t("console.dashboard.systemInfo")}
            </h3>
            <dl className="space-y-2 text-xs">
              <div className="flex justify-between py-1 border-b border-[var(--border)]/60">
                <dt className="text-[var(--muted)]">{t("console.dashboard.environmentLabel")}</dt>
                <dd className="font-medium text-[var(--foreground)]">{t("app.environment")}</dd>
              </div>
              <div className="flex justify-between py-1 border-b border-[var(--border)]/60">
                <dt className="text-[var(--muted)]">{t("console.dashboard.runtimeLabel")}</dt>
                <dd className="font-medium text-[var(--foreground)]">Zhulong v0.1.0</dd>
              </div>
              <div className="flex justify-between py-1">
                <dt className="text-[var(--muted)]">Pipeline</dt>
                <dd className="font-medium text-[var(--positive)]">FFmpeg CGO Native</dd>
              </div>
            </dl>
          </div>
        </div>
      </div>
    </div>
  );
};
