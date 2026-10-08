import { z } from "zod";

export const interfaceInfoSchema = z.object({
  name: z.string(),
  mac: z.string(),
  linkUp: z.boolean(),
  mode: z.enum(["dhcp", "static"]),
  ipAddresses: z.array(z.string()).nullish().transform((val) => val ?? []),
  gateway: z.string().nullish().transform((val) => val ?? ""),
  dns: z.array(z.string()).nullish().transform((val) => val ?? []),
  isDefaultGw: z.boolean().default(false),
  isCurrent: z.boolean().default(false),
});

export type InterfaceInfo = z.infer<typeof interfaceInfoSchema>;

export const interfaceConfigSchema = z.object({
  mode: z.enum(["dhcp", "static"]),
  ipAddress: z.string().nullish().transform((val) => val ?? ""),
  subnetMask: z.string().nullish().transform((val) => val ?? ""),
  gateway: z.string().nullish().transform((val) => val ?? ""),
  dns: z.array(z.string()).nullish().transform((val) => val ?? []),
  setDefault: z.boolean().default(false),
});

export type InterfaceConfig = z.infer<typeof interfaceConfigSchema>;

export const applyResponseSchema = z.object({
  transactionId: z.string(),
  timeoutSec: z.number(),
  targetUrl: z.string(),
  confirmToken: z.string(),
});

export type ApplyResponse = z.infer<typeof applyResponseSchema>;

export const pingResponseSchema = z.object({
  reachable: z.boolean(),
  rttMs: z.number(),
});

export type PingResponse = z.infer<typeof pingResponseSchema>;

export const transactionStateSchema = z.object({
  transactionId: z.string(),
  status: z.string(),
  interfaceName: z.string(),
  confirmToken: z.string(),
  targetUrl: z.string(),
  timeoutSec: z.number(),
  expiresAt: z.string(),
  rollbackConfig: interfaceConfigSchema,
}).nullable();

export type TransactionState = z.infer<typeof transactionStateSchema>;
