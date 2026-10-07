import type { SupportedLanguage } from "@/shared/i18n";
import { z } from "zod";

export const API_REQUEST_TIMEOUT_MS = 10_000;

const apiEnvelopeSchema = z.object({
  code: z.string(),
  message: z.string(),
  data: z.unknown(),
});

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export type UnauthorizedHandler = () => void;
const unauthorizedListeners = new Set<UnauthorizedHandler>();

export function onUnauthorized(handler: UnauthorizedHandler): () => void {
  unauthorizedListeners.add(handler);
  return () => {
    unauthorizedListeners.delete(handler);
  };
}

function notifyUnauthorized(): void {
  for (const listener of unauthorizedListeners) {
    try {
      listener();
    } catch {
      // Ignore listener runtime errors
    }
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  body?: unknown;
  language: SupportedLanguage;
  signal?: AbortSignal;
  notifyOnUnauthorized?: boolean;
}

export interface ApiCallOptions {
  signal?: AbortSignal;
  notifyOnUnauthorized?: boolean;
}

function parseCallOptions(signalOrOptions?: AbortSignal | ApiCallOptions): ApiCallOptions {
  if (!signalOrOptions) return {};
  if (signalOrOptions instanceof AbortSignal) {
    return { signal: signalOrOptions };
  }
  return signalOrOptions;
}

async function requestApiData<T>(
  path: string,
  schema: z.ZodType<T, z.ZodTypeDef, unknown>,
  options: RequestOptions,
): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, API_REQUEST_TIMEOUT_MS);
  const abortRequest = () => controller.abort();

  if (options.signal?.aborted) {
    abortRequest();
  } else {
    options.signal?.addEventListener("abort", abortRequest, { once: true });
  }

  const headers: Record<string, string> = {
    Accept: "application/json",
    "Accept-Language": options.language,
  };
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }

  try {
    const response = await fetch(path, {
      method: options.method ?? "GET",
      credentials: "include",
      signal: controller.signal,
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    });

    const shouldNotify =
      options.notifyOnUnauthorized ??
      (!path.includes("/auth/login") && !path.includes("/auth/status") && !path.includes("/auth/me"));
    if (response.status === 401 && shouldNotify) {
      notifyUnauthorized();
    }

    let body: unknown;
    try {
      body = await response.json();
    } catch (error) {
      if (options.signal?.aborted) throw error;
      throw new ApiError("Invalid server response", response.status, "INVALID_RESPONSE");
    }

    const envelope = apiEnvelopeSchema.safeParse(body);
    if (!envelope.success) {
      if (import.meta.env?.DEV) {
        console.error(`[API] Invalid envelope structure for ${path}:`, envelope.error);
      }
      throw new ApiError("Invalid server response", response.status, "INVALID_RESPONSE");
    }
    if (!response.ok || envelope.data.code !== "OK") {
      throw new ApiError(envelope.data.message, response.status, envelope.data.code);
    }

    const data = schema.safeParse(envelope.data.data);
    if (!data.success) {
      if (import.meta.env?.DEV) {
        console.error(`[API] Schema validation failed for ${path}:`, data.error);
      }
      throw new ApiError("Invalid server response", response.status, "INVALID_RESPONSE");
    }
    return data.data;
  } catch (error) {
    if (timedOut) {
      throw new ApiError("Request timed out", 0, "TIMEOUT");
    }
    throw error;
  } finally {
    clearTimeout(timeoutId);
    options.signal?.removeEventListener("abort", abortRequest);
  }
}

export function getApiData<T>(
  path: string,
  schema: z.ZodType<T, z.ZodTypeDef, unknown>,
  language: SupportedLanguage,
  signalOrOptions?: AbortSignal | ApiCallOptions,
): Promise<T> {
  const opts = parseCallOptions(signalOrOptions);
  return requestApiData(path, schema, {
    method: "GET",
    language,
    signal: opts.signal,
    notifyOnUnauthorized: opts.notifyOnUnauthorized,
  });
}

export function postApiData<T>(
  path: string,
  bodyData: unknown,
  schema: z.ZodType<T, z.ZodTypeDef, unknown>,
  language: SupportedLanguage,
  signalOrOptions?: AbortSignal | ApiCallOptions,
): Promise<T> {
  const opts = parseCallOptions(signalOrOptions);
  return requestApiData(path, schema, {
    method: "POST",
    body: bodyData,
    language,
    signal: opts.signal,
    notifyOnUnauthorized: opts.notifyOnUnauthorized,
  });
}

export function putApiData<T>(
  path: string,
  bodyData: unknown,
  schema: z.ZodType<T, z.ZodTypeDef, unknown>,
  language: SupportedLanguage,
  signalOrOptions?: AbortSignal | ApiCallOptions,
): Promise<T> {
  const opts = parseCallOptions(signalOrOptions);
  return requestApiData(path, schema, {
    method: "PUT",
    body: bodyData,
    language,
    signal: opts.signal,
    notifyOnUnauthorized: opts.notifyOnUnauthorized,
  });
}

export function deleteApiData<T>(
  path: string,
  schema: z.ZodType<T, z.ZodTypeDef, unknown>,
  language: SupportedLanguage,
  signalOrOptions?: AbortSignal | ApiCallOptions,
): Promise<T> {
  const opts = parseCallOptions(signalOrOptions);
  return requestApiData(path, schema, {
    method: "DELETE",
    language,
    signal: opts.signal,
    notifyOnUnauthorized: opts.notifyOnUnauthorized,
  });
}

export { createPaginatedSchema, type PaginatedData } from "./pagination";


