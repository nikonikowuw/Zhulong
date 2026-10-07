import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import { Check, Copy, Globe, Network, SlidersHorizontal, Wifi, WifiOff } from "lucide-react";
import type { InterfaceInfo } from "../types";

interface NetworkCardProps {
  iface: InterfaceInfo;
  onEdit: (iface: InterfaceInfo) => void;
}

export const NetworkCard: FC<NetworkCardProps> = ({ iface, onEdit }) => {
  const { t } = useTranslation();
  const [copiedMac, setCopiedMac] = useState(false);

  async function handleCopyMac() {
    try {
      await navigator.clipboard.writeText(iface.mac);
      setCopiedMac(true);
      setTimeout(() => setCopiedMac(false), 2000);
    } catch {
      // clipboard write ignored
    }
  }

  const primaryIp = iface.ipAddresses[0] ?? "-";

  return (
    <div
      className={`relative flex flex-col justify-between rounded-xl border p-5 transition-shadow ${
        iface.isCurrent
          ? "border-[var(--accent)] bg-[var(--surface)] shadow-sm"
          : "border-[var(--border)] bg-[var(--surface)] hover:border-[var(--border-strong)]"
      }`}
    >
      <div>
        {/* Card Header */}
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2.5">
            <div
              className={`flex h-10 w-10 items-center justify-center rounded-lg ${
                iface.linkUp
                  ? "bg-[var(--positive-soft)] text-[var(--positive)]"
                  : "bg-[var(--surface-muted)] text-[var(--muted)]"
              }`}
            >
              <Network size={20} />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="text-base font-semibold text-[var(--foreground)]">{iface.name}</span>
                {iface.isCurrent && (
                  <span
                    className="inline-flex items-center rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-xs font-medium text-[var(--accent)]"
                    title={t("systemSettings.network.currentInterfaceHint")}
                  >
                    {t("systemSettings.network.currentInterface")}
                  </span>
                )}
                {iface.isDefaultGw && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-[var(--surface-muted)] px-2 py-0.5 text-xs font-medium text-[var(--muted)] border border-[var(--border)]">
                    <Globe size={11} />
                    {t("systemSettings.network.defaultGateway")}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-1.5 text-xs text-[var(--muted)] font-mono mt-0.5">
                <span>{iface.mac || "--:--:--:--:--:--"}</span>
                {iface.mac && (
                  <button
                    type="button"
                    onClick={handleCopyMac}
                    className="text-[var(--muted)] hover:text-[var(--foreground)] p-0.5 rounded transition-colors"
                    title={t("systemSettings.network.actions.copyMac")}
                    aria-label={t("systemSettings.network.actions.copyMac")}
                  >
                    {copiedMac ? <Check size={12} className="text-[var(--positive)]" /> : <Copy size={12} />}
                  </button>
                )}
              </div>
            </div>
          </div>

          {/* Status Badges */}
          <div className="flex items-center gap-2">
            <span
              className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ${
                iface.linkUp
                  ? "bg-[var(--positive-soft)] text-[var(--positive)]"
                  : "bg-[var(--surface-muted)] text-[var(--muted)]"
              }`}
            >
              {iface.linkUp ? <Wifi size={12} /> : <WifiOff size={12} />}
              {iface.linkUp
                ? t("systemSettings.network.status.linkUp")
                : t("systemSettings.network.status.linkDown")}
            </span>
            <span className="rounded-md bg-[var(--surface-muted)] px-2 py-0.5 text-xs font-mono font-medium text-[var(--muted)] border border-[var(--border)]">
              {iface.mode === "dhcp"
                ? t("systemSettings.network.status.modeDhcp")
                : t("systemSettings.network.status.modeStatic")}
            </span>
          </div>
        </div>

        {/* Card Details Grid */}
        <div className="grid grid-cols-2 gap-3 rounded-lg border border-[var(--border-subtle)] bg-[var(--surface-muted)] p-3 text-xs">
          <div>
            <span className="text-[var(--muted)]">{t("systemSettings.network.fields.ipAddress")}</span>
            <div className="font-mono font-medium text-[var(--foreground)] mt-0.5 truncate" title={primaryIp}>
              {primaryIp}
            </div>
          </div>
          <div>
            <span className="text-[var(--muted)]">{t("systemSettings.network.fields.gateway")}</span>
            <div className="font-mono font-medium text-[var(--foreground)] mt-0.5 truncate" title={iface.gateway || "-"}>
              {iface.gateway || "-"}
            </div>
          </div>
          <div className="col-span-2">
            <span className="text-[var(--muted)]">{t("systemSettings.network.fields.dns")}</span>
            <div
              className="font-mono font-medium text-[var(--foreground)] mt-0.5 truncate"
              title={iface.dns.length > 0 ? iface.dns.join(", ") : "-"}
            >
              {iface.dns.length > 0 ? iface.dns.join(", ") : "-"}
            </div>
          </div>
        </div>
      </div>

      {/* Card Action Footer */}
      <div className="mt-4 flex items-center justify-end">
        <button
          type="button"
          onClick={() => onEdit(iface)}
          className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] transition-colors hover:bg-[var(--surface-hover)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)]"
        >
          <SlidersHorizontal size={13} />
          {t("systemSettings.network.actions.configure")}
        </button>
      </div>
    </div>
  );
};
