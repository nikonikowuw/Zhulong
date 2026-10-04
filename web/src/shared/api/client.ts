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

export async function getApiData<T>(
  path: string,
  schema: z.ZodType<T>,
  language: SupportedLanguage,
  signal?: AbortSignal,
): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, API_REQUEST_TIMEOUT_MS);
  const abortRequest = () => controller.abort();

  if (signal?.aborted) {
    abortRequest();
  } else {
    signal?.addEventListener("abort", abortRequest, { once: true });
  }

  try {
    const response = await fetch(path, {
      signal: controller.signal,
      headers: {
        Accept: "application/json",
        "Accept-Language": language,
      },
    });

    let body: unknown;
    try {
      body = await response.json();
    } catch (error) {
      if (signal?.aborted) throw error;
      throw new ApiError("Invalid server response", response.status, "INVALID_RESPONSE");
    }

    const envelope = apiEnvelopeSchema.safeParse(body);
    if (!envelope.success) {
      throw new ApiError("Invalid server response", response.status, "INVALID_RESPONSE");
    }
    if (!response.ok || envelope.data.code !== "OK") {
      throw new ApiError(envelope.data.message, response.status, envelope.data.code);
    }

    const data = schema.safeParse(envelope.data.data);
    if (!data.success) {
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
    signal?.removeEventListener("abort", abortRequest);
  }
}
