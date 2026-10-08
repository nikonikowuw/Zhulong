import { useEffect, useRef, useState, type FC, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  HelpCircle,
  Loader2,
  Radio,
  Send,
  X,
} from "lucide-react";
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
  const modalRef = useRef<HTMLDivElement>(null);
  const firstInputRef = useRef<HTMLInputElement>(null);

  const initialIp = iface.ipAddresses[0]?.split("/")[0] ?? "";
  const [mode, setMode] = useState<"dhcp" | "static">(iface.mode);
  const [ipAddress, setIpAddress] = useState(initialIp);
  const [subnetMask, setSubnetMask] = useState("255.255.255.0");
  const [gateway, setGateway] = useState(iface.gateway || "");
  const [dnsInput, setDnsInput] = useState(iface.dns?.join(", ") ?? "");
  const [setDefault, setSetDefault] = useState(iface.isDefaultGw);

  const [fieldErrors, setFieldErrors] = useState<{
    ipAddress?: string;
    subnetMask?: string;
    gateway?: string;
    dns?: string;
  }>({});

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

  useEffect(() => {
    if (isOpen && mode === "static") {
      firstInputRef.current?.focus();
    }
  }, [isOpen, mode]);

  if (!isOpen) return null;

  const otherDefaultGwIface = allInterfaces.find(
    (item) => item.name !== iface.name && item.isDefaultGw,
  );

  function validateIp(val: string): string | undefined {
    if (!val.trim()) return t("systemSettings.network.validation.requiredField");
    if (!isValidIPv4(val)) return t("systemSettings.network.validation.invalidIp");
    return undefined;
  }

  function validateSubnet(val: string): string | undefined {
    if (!val.trim()) return t("systemSettings.network.validation.requiredField");
    if (!isValidIPv4(val)) return t("systemSettings.network.validation.invalidSubnet");
    return undefined;
  }

  function validateGw(val: string, isDef: boolean): string | undefined {
    if (isDef && !val.trim()) return t("systemSettings.network.validation.gatewayRequired");
    if (val.trim() && !isValidIPv4(val)) return t("systemSettings.network.validation.invalidGateway");
    return undefined;
  }

  function validateDns(val: string): string | undefined {
    if (!val.trim()) return undefined;
    const parts = val.split(",").map((s) => s.trim()).filter(Boolean);
    for (const p of parts) {
      if (!isValidIPv4(p)) return t("systemSettings.network.validation.invalidDns");
    }
    return undefined;
  }

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
      const ipErr = validateIp(ipAddress);
      const subnetErr = validateSubnet(subnetMask);
      const gwErr = validateGw(gateway, setDefault);
      const dnsErr = validateDns(dnsInput);

      setFieldErrors({
        ipAddress: ipErr,
        subnetMask: subnetErr,
        gateway: gwErr,
        dns: dnsErr,
      });

      if (ipErr || subnetErr || gwErr || dnsErr) {
        return;
      }
    } else {
      const dnsErr = validateDns(dnsInput);
      if (dnsErr) {
        setFieldErrors({ dns: dnsErr });
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
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm animate-in fade-in"
      role="dialog"
      aria-modal="true"
      aria-labelledby="network-edit-title"
    >
      <div
        ref={modalRef}
        className="relative w-full max-w-lg rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-2xl ring-1 ring-black/5 dark:ring-white/10 max-h-[90vh] overflow-y-auto"
      >
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-[var(--border)]">
          <div>
            <div className="flex items-center gap-2">
              <h2 id="network-edit-title" className="text-lg font-bold tracking-tight text-[var(--foreground)]">
                {t("systemSettings.network.actions.configure")}: {iface.name}
              </h2>
              {iface.isCurrent && (
                <span className="rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-[10px] font-semibold text-[var(--accent)] border border-[var(--accent)]/30">
                  {t("systemSettings.network.currentInterface")}
                </span>
              )}
            </div>
            <p className="text-xs text-[var(--muted)] font-mono mt-0.5">MAC: {iface.mac || "--:--:--:--:--:--"}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label={t("systemSettings.network.actions.cancel")}
            className="rounded-lg p-1.5 text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)] transition-colors cursor-pointer"
          >
            <X size={18} aria-hidden="true" />
          </button>
        </div>

        {/* Global Error Banner */}
        {errorMsg && (
          <div
            role="alert"
            tabIndex={-1}
            className="mt-4 flex items-center gap-2 rounded-xl bg-[var(--negative-soft)] p-3 text-xs text-[var(--negative)] border border-[var(--negative)]/25 animate-in fade-in"
          >
            <AlertCircle size={15} className="shrink-0" aria-hidden="true" />
            <span className="font-medium">{errorMsg}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="mt-4 space-y-4">
          {/* Mode Switch (DHCP / Static) */}
          <div>
            <label className="block text-xs font-semibold text-[var(--foreground)] mb-2">
              {t("systemSettings.network.form.modeLabel", { name: iface.name })}
            </label>
            <div className="grid grid-cols-2 gap-3" aria-label="IP Assignment Mode">
              <button
                type="button"
                aria-pressed={mode === "dhcp"}
                onClick={() => {
                  setMode("dhcp");
                  setFieldErrors({});
                }}
                className={`flex items-center justify-center gap-2 rounded-xl border py-2.5 text-xs font-semibold transition-all cursor-pointer ${
                  mode === "dhcp"
                    ? "border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)] shadow-2xs"
                    : "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)]"
                }`}
              >
                <Radio size={14} aria-hidden="true" />
                <span>{t("systemSettings.network.status.modeDhcp")}</span>
              </button>
              <button
                type="button"
                aria-pressed={mode === "static"}
                onClick={() => {
                  setMode("static");
                  setFieldErrors({});
                }}
                className={`flex items-center justify-center gap-2 rounded-xl border py-2.5 text-xs font-semibold transition-all cursor-pointer ${
                  mode === "static"
                    ? "border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)] shadow-2xs"
                    : "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--foreground)]"
                }`}
              >
                <Radio size={14} aria-hidden="true" />
                <span>{t("systemSettings.network.status.modeStatic")}</span>
              </button>
            </div>
          </div>

          {/* Static Fields Box */}
          {mode === "static" && (
            <div className="space-y-3.5 rounded-2xl border border-[var(--border-subtle)] bg-[var(--surface-muted)]/80 p-4 transition-all">
              {/* IP Address */}
              <div>
                <label htmlFor="modal-ip-address" className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.ipAddress")} <span className="text-[var(--negative)]">*</span>
                </label>
                <input
                  id="modal-ip-address"
                  ref={firstInputRef}
                  type="text"
                  required
                  value={ipAddress}
                  onChange={(e) => {
                    setIpAddress(e.target.value);
                    if (fieldErrors.ipAddress) {
                      setFieldErrors((prev) => ({ ...prev, ipAddress: undefined }));
                    }
                  }}
                  onBlur={() => {
                    setFieldErrors((prev) => ({ ...prev, ipAddress: validateIp(ipAddress) }));
                  }}
                  placeholder="192.168.1.100"
                  aria-invalid={Boolean(fieldErrors.ipAddress)}
                  aria-describedby={fieldErrors.ipAddress ? "modal-ip-error" : undefined}
                  className={`w-full rounded-lg border bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--foreground)] transition-colors focus:outline-none ${
                    fieldErrors.ipAddress
                      ? "border-[var(--danger)] focus:border-[var(--danger)] ring-1 ring-[var(--danger)]/30"
                      : "border-[var(--border)] focus:border-[var(--accent)]"
                  }`}
                />
                {fieldErrors.ipAddress && (
                  <p id="modal-ip-error" className="mt-1 text-[11px] text-[var(--danger)] flex items-center gap-1">
                    <AlertCircle size={11} aria-hidden="true" />
                    <span>{fieldErrors.ipAddress}</span>
                  </p>
                )}
              </div>

              {/* Subnet Mask */}
              <div>
                <label htmlFor="modal-subnet-mask" className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.subnetMask")} <span className="text-[var(--negative)]">*</span>
                </label>
                <input
                  id="modal-subnet-mask"
                  type="text"
                  required
                  value={subnetMask}
                  onChange={(e) => {
                    setSubnetMask(e.target.value);
                    if (fieldErrors.subnetMask) {
                      setFieldErrors((prev) => ({ ...prev, subnetMask: undefined }));
                    }
                  }}
                  onBlur={() => {
                    setFieldErrors((prev) => ({ ...prev, subnetMask: validateSubnet(subnetMask) }));
                  }}
                  placeholder="255.255.255.0"
                  aria-invalid={Boolean(fieldErrors.subnetMask)}
                  aria-describedby={fieldErrors.subnetMask ? "modal-subnet-error" : undefined}
                  className={`w-full rounded-lg border bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--foreground)] transition-colors focus:outline-none ${
                    fieldErrors.subnetMask
                      ? "border-[var(--danger)] focus:border-[var(--danger)] ring-1 ring-[var(--danger)]/30"
                      : "border-[var(--border)] focus:border-[var(--accent)]"
                  }`}
                />
                {fieldErrors.subnetMask && (
                  <p id="modal-subnet-error" className="mt-1 text-[11px] text-[var(--danger)] flex items-center gap-1">
                    <AlertCircle size={11} aria-hidden="true" />
                    <span>{fieldErrors.subnetMask}</span>
                  </p>
                )}
              </div>

              {/* Gateway */}
              <div>
                <label htmlFor="modal-gateway" className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.gateway")}
                </label>
                <input
                  id="modal-gateway"
                  type="text"
                  value={gateway}
                  onChange={(e) => {
                    setGateway(e.target.value);
                    if (fieldErrors.gateway) {
                      setFieldErrors((prev) => ({ ...prev, gateway: undefined }));
                    }
                  }}
                  onBlur={() => {
                    setFieldErrors((prev) => ({ ...prev, gateway: validateGw(gateway, setDefault) }));
                  }}
                  placeholder="192.168.1.1"
                  aria-invalid={Boolean(fieldErrors.gateway)}
                  aria-describedby={fieldErrors.gateway ? "modal-gw-error" : undefined}
                  className={`w-full rounded-lg border bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--foreground)] transition-colors focus:outline-none ${
                    fieldErrors.gateway
                      ? "border-[var(--danger)] focus:border-[var(--danger)] ring-1 ring-[var(--danger)]/30"
                      : "border-[var(--border)] focus:border-[var(--accent)]"
                  }`}
                />
                {fieldErrors.gateway && (
                  <p id="modal-gw-error" className="mt-1 text-[11px] text-[var(--danger)] flex items-center gap-1">
                    <AlertCircle size={11} aria-hidden="true" />
                    <span>{fieldErrors.gateway}</span>
                  </p>
                )}
              </div>

              {/* DNS Servers */}
              <div>
                <label htmlFor="modal-dns" className="block text-xs font-medium text-[var(--foreground)] mb-1">
                  {t("systemSettings.network.fields.dns")} {t("systemSettings.network.form.commaSeparated")}
                </label>
                <input
                  id="modal-dns"
                  type="text"
                  value={dnsInput}
                  onChange={(e) => {
                    setDnsInput(e.target.value);
                    if (fieldErrors.dns) {
                      setFieldErrors((prev) => ({ ...prev, dns: undefined }));
                    }
                  }}
                  onBlur={() => {
                    setFieldErrors((prev) => ({ ...prev, dns: validateDns(dnsInput) }));
                  }}
                  placeholder="223.5.5.5, 8.8.8.8"
                  aria-invalid={Boolean(fieldErrors.dns)}
                  aria-describedby={fieldErrors.dns ? "modal-dns-error" : undefined}
                  className={`w-full rounded-lg border bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--foreground)] transition-colors focus:outline-none ${
                    fieldErrors.dns
                      ? "border-[var(--danger)] focus:border-[var(--danger)] ring-1 ring-[var(--danger)]/30"
                      : "border-[var(--border)] focus:border-[var(--accent)]"
                  }`}
                />
                {fieldErrors.dns && (
                  <p id="modal-dns-error" className="mt-1 text-[11px] text-[var(--danger)] flex items-center gap-1">
                    <AlertCircle size={11} aria-hidden="true" />
                    <span>{fieldErrors.dns}</span>
                  </p>
                )}
              </div>

              {/* Single Default Gateway Checkbox & Warning */}
              <div className="pt-2 border-t border-[var(--border-subtle)]">
                <label className="flex items-center gap-2.5 cursor-pointer text-xs font-semibold text-[var(--foreground)] select-none">
                  <input
                    type="checkbox"
                    checked={setDefault}
                    onChange={(e) => {
                      setSetDefault(e.target.checked);
                      if (e.target.checked && fieldErrors.gateway) {
                        setFieldErrors((prev) => ({ ...prev, gateway: validateGw(gateway, true) }));
                      }
                    }}
                    className="h-4 w-4 rounded border-[var(--border)] text-[var(--accent)] focus:ring-[var(--accent)] cursor-pointer"
                  />
                  <span>{t("systemSettings.network.fields.setDefaultGateway")}</span>
                </label>
                {setDefault && otherDefaultGwIface && (
                  <div className="mt-2 flex items-start gap-2 rounded-xl bg-[var(--warning-soft)] p-2.5 text-[11px] text-[var(--warning-strong)] border border-[var(--warning)]/20 animate-in fade-in">
                    <HelpCircle size={14} className="shrink-0 mt-0.5" aria-hidden="true" />
                    <span>
                      {t("systemSettings.network.validation.gatewayConflict")} ({otherDefaultGwIface.name})
                    </span>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* Dedicated Ping Diagnostics Tool */}
          <div className="rounded-2xl border border-[var(--border-subtle)] bg-[var(--surface-muted)]/70 p-4">
            <div className="flex items-center gap-2 mb-2">
              <Activity size={14} className="text-[var(--accent)]" aria-hidden="true" />
              <span className="text-xs font-bold text-[var(--foreground)]">
                {t("systemSettings.network.ping.title")}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={pingTarget}
                onChange={(e) => setPingTarget(e.target.value)}
                placeholder={t("systemSettings.network.ping.placeholder")}
                aria-label={t("systemSettings.network.ping.title")}
                className="flex-1 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 font-mono text-xs text-[var(--foreground)] focus:border-[var(--accent)] focus:outline-none"
              />
              <button
                type="button"
                onClick={handlePing}
                disabled={pingMutation.isPending}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3.5 py-2 text-xs font-semibold text-[var(--foreground)] hover:bg-[var(--surface-hover)] disabled:opacity-50 transition-all cursor-pointer shadow-2xs"
              >
                {pingMutation.isPending ? (
                  <Loader2 size={12} className="animate-spin" aria-hidden="true" />
                ) : (
                  <Send size={12} aria-hidden="true" />
                )}
                <span>
                  {pingMutation.isPending
                    ? t("systemSettings.network.actions.pinging")
                    : t("systemSettings.network.actions.ping")}
                </span>
              </button>
            </div>
            {pingResult && (
              <div
                className={`mt-2.5 flex items-center gap-1.5 text-xs font-medium p-2 rounded-lg border ${
                  pingResult.reachable
                    ? "border-[var(--positive)]/20 bg-[var(--positive-soft)] text-[var(--positive)]"
                    : "border-[var(--negative)]/20 bg-[var(--negative-soft)] text-[var(--negative)]"
                }`}
              >
                {pingResult.reachable ? (
                  <CheckCircle2 size={14} aria-hidden="true" />
                ) : (
                  <AlertCircle size={14} aria-hidden="true" />
                )}
                <span>
                  {pingResult.reachable
                    ? t("systemSettings.network.ping.reachable", { rtt: pingResult.rttMs })
                    : t("systemSettings.network.ping.unreachable")}
                </span>
              </div>
            )}
          </div>

          {/* Form Actions Footer */}
          <div className="flex items-center justify-end gap-2.5 pt-4 border-t border-[var(--border)]">
            <button
              type="button"
              onClick={onClose}
              disabled={isApplying}
              className="rounded-xl border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-xs font-semibold text-[var(--foreground)] hover:bg-[var(--surface-hover)] transition-colors cursor-pointer"
            >
              {t("systemSettings.network.actions.cancel")}
            </button>
            <button
              type="submit"
              disabled={isApplying}
              className="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent)] px-4 py-2 text-xs font-semibold text-white shadow-sm hover:opacity-90 active:scale-[0.98] disabled:opacity-50 transition-all cursor-pointer"
            >
              {isApplying && <Loader2 size={13} className="animate-spin" aria-hidden="true" />}
              <span>{t("systemSettings.network.actions.apply")}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
