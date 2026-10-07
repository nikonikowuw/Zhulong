import { useEffect, useState, type FC, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, CheckCircle2, Loader2, Radio, Send, X } from "lucide-react";
import { usePingTargetMutation } from "../hooks/useNetwork";
import type { InterfaceConfig, InterfaceInfo, PingResponse } from "../types";

interface NetworkEditModalProps {
  iface: InterfaceInfo;
  allInterfaces: InterfaceInfo[];
  isOpen: boolean;
  onClose: () => void;
  onApply: (ifaceName: string, config: InterfaceConfig) => Promise<void>;
  isApplying: boolean;
}

const IPV4_REGEX = /^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9]?)$/;

function isValidIPv4(ip: string): boolean {
  return IPV4_REGEX.test(ip.trim());
}

export const NetworkEditModal: FC<NetworkEditModalProps> = ({
  iface,
  allInterfaces,
  isOpen,
  onClose,
  onApply,
  isApplying,
}) => {
  const { t } = useTranslation();
  const pingMutation = usePingTargetMutation();

  const initialIp = iface.ipAddresses[0]?.split("/")[0] ?? "";
  const [mode, setMode] = useState<"dhcp" | "static">(iface.mode);
  const [ipAddress, setIpAddress] = useState(initialIp);
  const [subnetMask, setSubnetMask] = useState("255.255.255.0");
  const [gateway, setGateway] = useState(iface.gateway || "");
  const [dnsInput, setDnsInput] = useState(iface.dns.join(", "));
  const [setDefault, setSetDefault] = useState(iface.isDefaultGw);

  const [pingTarget, setPingTarget] = useState(iface.gateway || "192.168.1.1");
  const [pingResult, setPingResult] = useState<PingResponse | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const otherDefaultGwIface = allInterfaces.find(
    (item) => item.name !== iface.name && item.isDefaultGw,
  );

  async function handlePing() {
    if (!pingTarget.trim() || !isValidIPv4(pingTarget.trim())) {
      setErrorMsg(t("systemSettings.network.validation.invalidIp"));
      return;
    }
    setErrorMsg(null);
    try {
      const res = await pingMutation.mutateAsync(pingTarget.trim());
      setPingResult(res);
    } catch {
      setPingResult({ reachable: false, rttMs: 0 });
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setErrorMsg(null);

    const dnsList = dnsInput
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    if (mode === "static") {
      if (!isValidIPv4(ipAddress)) {
        setErrorMsg(t("systemSettings.network.validation.invalidIp"));
        return;
      }
      if (!isValidIPv4(subnetMask)) {
        setErrorMsg(t("systemSettings.network.validation.invalidSubnet"));
        return;
      }
      if (gateway.trim() && !isValidIPv4(gateway.trim())) {
        setErrorMsg(t("systemSettings.network.validation.invalidGateway"));
        return;
      }
      if (setDefault && !gateway.trim()) {
        setErrorMsg(t("systemSettings.network.validation.gatewayRequired"));
        return;
      }
    }

    for (const d of dnsList) {
      if (!isValidIPv4(d)) {
        setErrorMsg(t("systemSettings.network.validation.invalidDns"));
        return;
      }
    }

    const config: InterfaceConfig = {
      mode,
      ipAddress: mode === "static" ? ipAddress.trim() : "",
      subnetMask: mode === "static" ? subnetMask.trim() : "",
      gateway: mode === "static" ? gateway.trim() : "",
      dns: dnsList,
      setDefault: setDefault,
    };

    try {
      await onApply(iface.name, config);
    } catch (err: unknown) {
      if (err instanceof Error) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg("Failed to apply configuration");
      }
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm animate-in fade-in">
      <div className="relative w-full max-w-lg rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl ring-1 ring-black/5 dark:ring-white/10">
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-[var(--border)]">
          <div>
            <h2 className="text-lg font-semibold text-[var(--foreground)]">
              {t("systemSettings.network.actions.configure")}: {iface.name}
            </h2>
            <p className="text-xs text-[var(--muted)] font-mono mt-0.5">MAC: {iface.mac}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors"
          >
            <X size={18} />
          </button>
        </div>

        {/* Error Alert */}
        {errorMsg && (
          <div className="mt-4 flex items-center gap-2 rounded-lg bg-[var(--negative-soft)] p-3 text-xs text-[var(--negative)] border border-[var(--negative)]/20">
            <AlertCircle size={15} className="shrink-0" />
            <span>{errorMsg}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="mt-4 space-y-4">
          {/* Mode Switch (DHCP / Static) */}
          <div>
            <label className="block text-xs font-medium text-[var(--foreground)] mb-2">
              {t("systemSettings.network.form.modeLabel", { name: iface.name })}
            </label>
            <div className="grid grid-cols-2 gap-3">
              <button
                type="button"
                onClick={() => setMode("dhcp")}
                className={`flex items-center justify-center gap-2 rounded-lg border py-2.5 text-xs font-medium transition-all ${
                  mode === "dhcp"
                    ? "border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]"
                    : "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)] hover:bg-[var(--surface-hover)]"
                }`}
              >
                <Radio size={14} />
                {t("systemSettings.network.status.modeDhcp")}
              </button>
              <button
                type="button"
                onClick={() => setMode("static")}
                className={`flex items-center justify-center gap-2 rounded-lg border py-2.5 text-xs font-medium transition-all ${
                  mode === "static"
                    ? "border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]"
                    : "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)] hover:bg-[var(--surface-hover)]"
                }`}
              >
                <Radio size={14} />
                {t("systemSettings.network.status.modeStatic")}
              </button>
            </div>
          </div>

          {/* Static Fields */}
          {mode === "static" && (
            <div className="space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-muted)] p-3.5">
              <div>
                <label className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.ipAddress")} *
                </label>
                <input
                  type="text"
                  required
                  value={ipAddress}
                  onChange={(e) => setIpAddress(e.target.value)}
                  placeholder="192.168.1.100"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.subnetMask")} *
                </label>
                <input
                  type="text"
                  required
                  value={subnetMask}
                  onChange={(e) => setSubnetMask(e.target.value)}
                  placeholder="255.255.255.0"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.gateway")}
                </label>
                <input
                  type="text"
                  value={gateway}
                  onChange={(e) => setGateway(e.target.value)}
                  placeholder="192.168.1.1"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.dns")} {t("systemSettings.network.form.commaSeparated")}
                </label>
                <input
                  type="text"
                  value={dnsInput}
                  onChange={(e) => setDnsInput(e.target.value)}
                  placeholder="223.5.5.5, 8.8.8.8"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
                />
              </div>

              {/* Default Gateway Checkbox */}
              <div className="pt-1">
                <label className="flex items-center gap-2 cursor-pointer text-xs font-medium text-[var(--foreground)]">
                  <input
                    type="checkbox"
                    checked={setDefault}
                    onChange={(e) => setSetDefault(e.target.checked)}
                    className="rounded border-[var(--border)] text-[var(--accent)] focus:ring-[var(--accent)]"
                  />
                  <span>{t("systemSettings.network.fields.setDefaultGateway")}</span>
                </label>
                {setDefault && otherDefaultGwIface && (
                  <p className="mt-1 text-[11px] text-[var(--warning)] flex items-center gap-1">
                    <AlertCircle size={12} />
                    {t("systemSettings.network.validation.gatewayConflict")} ({otherDefaultGwIface.name})
                  </p>
                )}
              </div>
            </div>
          )}

          {/* Ping Connectivity Diagnostic */}
          <div className="rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-muted)] p-3">
            <span className="block text-xs font-medium text-[var(--foreground)] mb-1.5">
              {t("systemSettings.network.ping.title")}
            </span>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={pingTarget}
                onChange={(e) => setPingTarget(e.target.value)}
                placeholder={t("systemSettings.network.ping.placeholder")}
                className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
              />
              <button
                type="button"
                onClick={handlePing}
                disabled={pingMutation.isPending}
                className="inline-flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] disabled:opacity-50"
              >
                {pingMutation.isPending ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Send size={12} />
                )}
                {pingMutation.isPending
                  ? t("systemSettings.network.actions.pinging")
                  : t("systemSettings.network.actions.ping")}
              </button>
            </div>
            {pingResult && (
              <div
                className={`mt-2 flex items-center gap-1.5 text-xs ${
                  pingResult.reachable ? "text-[var(--positive)]" : "text-[var(--negative)]"
                }`}
              >
                {pingResult.reachable ? (
                  <CheckCircle2 size={13} />
                ) : (
                  <AlertCircle size={13} />
                )}
                <span>
                  {pingResult.reachable
                    ? t("systemSettings.network.ping.reachable", { rtt: pingResult.rttMs })
                    : t("systemSettings.network.ping.unreachable")}
                </span>
              </div>
            )}
          </div>

          {/* Form Actions */}
          <div className="flex items-center justify-end gap-2.5 pt-3 border-t border-[var(--border)]">
            <button
              type="button"
              onClick={onClose}
              disabled={isApplying}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors"
            >
              {t("systemSettings.network.actions.cancel")}
            </button>
            <button
              type="submit"
              disabled={isApplying}
              className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent)] px-4 py-2 text-xs font-medium text-white shadow-sm hover:opacity-90 disabled:opacity-50 transition-opacity"
            >
              {isApplying && <Loader2 size={13} className="animate-spin" />}
              {t("systemSettings.network.actions.apply")}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
