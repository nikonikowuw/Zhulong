import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import { Network, Settings } from "lucide-react";
import { NetworkSettingsTab } from "./components/NetworkSettingsTab";

type SettingsTab = "network";

export const SystemSettingsPage: FC = () => {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<SettingsTab>("network");

  return (
    <div className="flex-1 overflow-y-auto p-6 md:p-8">
      <div className="w-full space-y-6">
        {/* Page Header */}
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--surface-muted)] text-[var(--accent)] border border-[var(--border)]">
            <Settings size={20} />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tight text-[var(--foreground)]">
              {t("systemSettings.title")}
            </h1>
            <p className="text-xs text-[var(--muted)]">
              {t("systemSettings.network.description")}
            </p>
          </div>
        </div>

        {/* Tab Navigation */}
        <div className="flex border-b border-[var(--border)] gap-6">
          <button
            type="button"
            onClick={() => setActiveTab("network")}
            className={`flex items-center gap-2 border-b-2 pb-3 text-xs font-semibold transition-colors ${
              activeTab === "network"
                ? "border-[var(--accent)] text-[var(--accent)]"
                : "border-transparent text-[var(--muted)] hover:text-[var(--foreground)]"
            }`}
          >
            <Network size={15} />
            <span>{t("systemSettings.tabs.network")}</span>
          </button>
        </div>

        {/* Tab Content */}
        <div>
          {activeTab === "network" && <NetworkSettingsTab />}
        </div>
      </div>
    </div>
  );
};
