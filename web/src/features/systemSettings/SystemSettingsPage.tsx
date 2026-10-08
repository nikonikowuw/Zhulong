import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import { Network, RefreshCw } from "lucide-react";
import { NetworkSettingsTab } from "./components/NetworkSettingsTab";
import { useNetworkInterfacesQuery } from "./hooks/useNetwork";

type SettingsTab = "network";

export const SystemSettingsPage: FC = () => {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<SettingsTab>("network");
  const { data: interfaces = [], isRefetching, refetch } = useNetworkInterfacesQuery();

  return (
    <div className="system-settings-view flex flex-col gap-6 w-full animate-in fade-in duration-200">
      {/* Page Header - Unified structure matching CameraPage, AuditLogPage, OverviewDashboard */}
      <section
        className="page-heading flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4"
        aria-labelledby="system-settings-title"
      >
        <div>
          <div className="flex items-center gap-2.5">
            <h1 id="system-settings-title" className="text-2xl font-bold tracking-tight text-[var(--foreground)]">
              {t("systemSettings.title")}
            </h1>
            {interfaces.length > 0 && (
              <span className="inline-flex items-center gap-1 rounded-full border border-[var(--border)] bg-[var(--surface-muted)] px-2.5 py-0.5 text-[11px] font-mono text-[var(--muted)]">
                <Network size={11} aria-hidden="true" />
                <span>
                  {interfaces.length} {t("systemSettings.network.metrics.interfacesCount")}
                </span>
              </span>
            )}
          </div>
          <p className="page-description text-xs text-[var(--muted)] mt-1">
            {t("systemSettings.description")}
          </p>
        </div>

        {/* Global Action Controls */}
        <div className="flex items-center gap-2.5">
          <button
            type="button"
            className="icon-button"
            onClick={() => void refetch()}
            disabled={isRefetching}
            aria-label={t("systemSettings.network.actions.refresh")}
            title={t("systemSettings.network.actions.refresh")}
          >
            <RefreshCw className={isRefetching ? "animate-spin" : undefined} size={16} />
          </button>
        </div>
      </section>

      {/* Sub-navigation Segmented Pills */}
      <nav
        className="inline-flex items-center gap-1 p-1 rounded-xl bg-[var(--surface-muted)] border border-[var(--border)] w-fit"
        aria-label={t("systemSettings.tabsLabel")}
      >
        <button
          type="button"
          onClick={() => setActiveTab("network")}
          className={`inline-flex items-center gap-2 rounded-lg px-3.5 py-1.5 text-xs font-medium transition-all ${
            activeTab === "network"
              ? "bg-[var(--surface)] text-[var(--foreground)] shadow-xs font-semibold"
              : "text-[var(--muted)] hover:text-[var(--foreground)] hover:bg-[var(--surface-hover)]"
          }`}
        >
          <Network size={14} className={activeTab === "network" ? "text-[var(--accent)]" : undefined} />
          <span>{t("systemSettings.tabs.network")}</span>
          {interfaces.length > 0 && (
            <span className="rounded-full bg-[var(--surface-muted)] px-1.5 py-0.2 text-[10px] font-mono text-[var(--muted)]">
              {interfaces.length}
            </span>
          )}
        </button>
      </nav>

      {/* Tab Content Area */}
      <div className="flex-1 w-full min-w-0">
        {activeTab === "network" && <NetworkSettingsTab />}
      </div>
    </div>
  );
};
