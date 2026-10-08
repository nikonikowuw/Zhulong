import { useState, type FC } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, Copy, Globe, Network, SlidersHorizontal, Wifi, WifiOff } from "lucide-react";
import type { InterfaceInfo } from "../types";

interface NetworkCardProps {
  iface: InterfaceInfo;
  onEdit: (iface: InterfaceInfo) => void;
}

export const NetworkCard: FC<NetworkCardProps> = ({ iface, onEdit }) => {
  const { t } = useTranslation();
  const [copiedMac, setCopiedMac] = useState(false);
  const [showAllIps, setShowAllIps] = useState(false);

  async function handleCopyMac() {
    try {
      await navigator.clipboard.writeText(iface.mac);
      setCopiedMac(true);
      setTimeout(() => setCopiedMac(false), 2000);
    } catch {
      // clipboard write ignored
    }
  }

  const primaryIp = iface.ipAddresses?.[0] ?? "-";
  const extraIps = (iface.ipAddresses ?? []).slice(1);
  const gatewayDisplay = iface.gateway || t("systemSettings.network.fields.noGateway");
  const dnsDisplay = iface.dns && iface.dns.length > 0
    ? iface.dns.join(", ")
    : t("systemSettings.network.fields.noDns");

  return (
    <article
      className={`network-card group relative flex flex-col justify-between rounded-2xl border p-5 shadow-xs transition-all duration-200 hover:shadow-md ${
        iface.isCurrent
          ? "border-[var(--accent)] bg-[var(--surface)] ring-2 ring-[var(--accent)]/20"
          : "border-[var(--border)] bg-[var(--surface)] hover:border-[var(--border-strong,var(--accent))]/50"
      }`}
      aria-labelledby={`network-title-${iface.name}`}
    >
      <div>
        {/* Card Header */}
        <div className="flex items-start justify-between gap-3 mb-4">
          <div className="flex items-center gap-3 min-w-0">
            {/* Hardware Port Icon with Status LED */}
            <div className="relative shrink-0">
              <div
                className={`flex h-11 w-11 items-center justify-center rounded-xl transition-colors ${
                  iface.linkUp
                    ? "bg-[var(--positive-soft)] text-[var(--positive)]"
                    : "bg-[var(--surface-muted)] text-[var(--muted)]"
                }`}
              >
                <Network size={20} aria-hidden="true" />
              </div>
              <span
                className="absolute -bottom-0.5 -right-0.5 flex h-3.5 w-3.5 items-center justify-center rounded-full bg-[var(--surface)] ring-2 ring-[var(--surface)]"
                title={iface.linkUp ? t("systemSettings.network.status.linkUp") : t("systemSettings.network.status.linkDown")}
              >
                <span
                  className={`h-2 w-2 rounded-full ${
                    iface.linkUp ? "bg-[var(--positive)] animate-pulse" : "bg-[var(--muted)]"
                  }`}
                />
              </span>
            </div>

            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-1.5">
                <span
                  id={`network-title-${iface.name}`}
                  className="text-base font-bold tracking-tight text-[var(--foreground)]"
                >
                  {iface.name}
                </span>
                {iface.isCurrent && (
                  <span
                    className="inline-flex items-center rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-[11px] font-semibold text-[var(--accent)] border border-[var(--accent)]/25"
                    title={t("systemSettings.network.currentInterfaceHint")}
                  >
                    {t("systemSettings.network.currentInterface")}
                  </span>
                )}
                {iface.isDefaultGw && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-[var(--surface-muted)] px-2 py-0.5 text-[11px] font-medium text-[var(--muted)] border border-[var(--border)]">
                    <Globe size={11} aria-hidden="true" />
                    <span>{t("systemSettings.network.defaultGateway")}</span>
                  </span>
                )}
              </div>

              {/* MAC Address with 1-click Copy */}
              <div className="flex items-center gap-1.5 text-xs text-[var(--muted)] font-mono mt-0.5">
                <span className="tracking-wide">{iface.mac || "--:--:--:--:--:--"}</span>
                {iface.mac && (
                  <button
                    type="button"
                    onClick={handleCopyMac}
                    className="inline-flex items-center justify-center text-[var(--muted)] hover:text-[var(--foreground)] p-1 rounded-md transition-colors hover:bg-[var(--surface-hover)] focus:outline-none focus:ring-1 focus:ring-[var(--focus)] cursor-pointer"
                    title={t("systemSettings.network.actions.copyMac")}
                    aria-label={t("systemSettings.network.actions.copyMac")}
                  >
                    {copiedMac ? (
                      <Check size={12} className="text-[var(--positive)]" />
                    ) : (
                      <Copy size={12} aria-hidden="true" />
                    )}
                  </button>
                )}
              </div>
            </div>
          </div>

          {/* Status Pills */}
          <div className="flex flex-col sm:flex-row items-end sm:items-center gap-1.5 shrink-0">
            <span
              className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium border ${
                iface.linkUp
                  ? "bg-[var(--positive-soft)] text-[var(--positive)] border-[var(--positive)]/20"
                  : "bg-[var(--surface-muted)] text-[var(--muted)] border-[var(--border)]"
              }`}
            >
              {iface.linkUp ? <Wifi size={12} aria-hidden="true" /> : <WifiOff size={12} aria-hidden="true" />}
              <span>
                {iface.linkUp
                  ? t("systemSettings.network.status.linkUp")
                  : t("systemSettings.network.status.linkDown")}
              </span>
            </span>
            <span className="rounded-md bg-[var(--surface-muted)] px-2 py-0.5 text-xs font-mono font-medium text-[var(--muted)] border border-[var(--border)]">
              {iface.mode === "dhcp"
                ? t("systemSettings.network.status.modeDhcp")
                : t("systemSettings.network.status.modeStatic")}
            </span>
          </div>
        </div>

        {/* Card Details Grid */}
        <div className="grid grid-cols-2 gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-muted)]/70 p-3.5 text-xs">
          <div>
            <span className="text-[var(--muted)] font-medium">
              {t("systemSettings.network.fields.ipAddress")}
            </span>
            <div
              className="font-mono font-semibold text-[var(--foreground)] mt-0.5 truncate tracking-tight text-xs sm:text-sm"
              title={primaryIp}
            >
              {primaryIp}
            </div>
            {extraIps.length > 0 && (
              <div className="mt-1">
                <button
                  type="button"
                  onClick={() => setShowAllIps(!showAllIps)}
                  className="inline-flex items-center gap-1 rounded bg-[var(--surface)] px-1.5 py-0.5 text-[10px] font-mono text-[var(--accent)] border border-[var(--border)] hover:bg-[var(--surface-hover)] transition-colors cursor-pointer"
                  title={extraIps.join(", ")}
                >
                  <span>{t("systemSettings.network.fields.additionalIps", { count: extraIps.length })}</span>
                  <ChevronDown
                    size={10}
                    className={`transition-transform duration-200 ${showAllIps ? "rotate-180" : ""}`}
                  />
                </button>
                {showAllIps && (
                  <div className="mt-1 space-y-0.5 pl-0.5 border-l-2 border-[var(--accent)]/40">
                    {extraIps.map((ip) => (
                      <div key={ip} className="font-mono text-[11px] text-[var(--muted)]">
                        {ip}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>

          <div>
            <span className="text-[var(--muted)] font-medium">
              {t("systemSettings.network.fields.gateway")}
            </span>
            <div
              className="font-mono font-semibold text-[var(--foreground)] mt-0.5 truncate text-xs sm:text-sm"
              title={iface.gateway || "-"}
            >
              {gatewayDisplay}
            </div>
          </div>

          <div className="col-span-2 pt-1 border-t border-[var(--border-subtle)]">
            <span className="text-[var(--muted)] font-medium">
              {t("systemSettings.network.fields.dns")}
            </span>
            <div
              className="font-mono text-xs text-[var(--foreground)] mt-0.5 truncate"
              title={iface.dns && iface.dns.length > 0 ? iface.dns.join(", ") : "-"}
            >
              {dnsDisplay}
            </div>
          </div>
        </div>
      </div>

      {/* Card Action Footer */}
      <div className="mt-4 flex items-center justify-end pt-3 border-t border-[var(--border-subtle)]">
        <button
          type="button"
          onClick={() => onEdit(iface)}
          className="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3.5 py-2 text-xs font-semibold text-[var(--foreground)] transition-all hover:bg-[var(--surface-hover)] hover:border-[var(--border-strong,var(--accent))] active:scale-[0.98] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] cursor-pointer shadow-2xs"
        >
          <SlidersHorizontal size={13} aria-hidden="true" />
          <span>{t("systemSettings.network.actions.configure")}</span>
        </button>
      </div>
    </article>
  );
};
