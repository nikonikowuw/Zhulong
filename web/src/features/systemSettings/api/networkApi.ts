import type { SupportedLanguage } from "@/shared/i18n";
import { getApiData, postApiData } from "@/shared/api/client";
import { z } from "zod";
import {
  applyResponseSchema,
  interfaceInfoSchema,
  pingResponseSchema,
  transactionStateSchema,
  type ApplyResponse,
  type InterfaceConfig,
  type InterfaceInfo,
  type PingResponse,
  type TransactionState,
} from "../types";

const interfaceListSchema = z.array(interfaceInfoSchema);
const statusEnvelopeSchema = z.object({
  status: z.string(),
});

export function fetchNetworkInterfaces(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<InterfaceInfo[]> {
  return getApiData("/api/v1/system/network/interfaces", interfaceListSchema, language, signal);
}

export function fetchNetworkStatus(
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<TransactionState> {
  return getApiData("/api/v1/system/network/status", transactionStateSchema, language, signal);
}

export function applyNetworkConfig(
  iface: string,
  config: InterfaceConfig,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<ApplyResponse> {
  return postApiData(
    `/api/v1/system/network/interfaces/${encodeURIComponent(iface)}/apply`,
    config,
    applyResponseSchema,
    language,
    signal,
  );
}

export function confirmNetworkConfig(
  token: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<{ status: string }> {
  const endpoint = token
    ? `/api/v1/system/network/confirm?token=${encodeURIComponent(token)}`
    : "/api/v1/system/network/confirm";
  return postApiData(endpoint, { token }, statusEnvelopeSchema, language, signal);
}

export function rollbackNetworkConfig(
  token: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<{ status: string }> {
  const endpoint = token
    ? `/api/v1/system/network/rollback?token=${encodeURIComponent(token)}`
    : "/api/v1/system/network/rollback";
  return postApiData(
    endpoint,
    { token },
    statusEnvelopeSchema,
    language,
    signal,
  );
}

export function pingNetworkTarget(
  target: string,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<PingResponse> {
  return postApiData(
    "/api/v1/system/network/ping",
    { target },
    pingResponseSchema,
    language,
    signal,
  );
}
