import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { API_REQUEST_TIMEOUT_MS, getApiData } from "./client";

const responseSchema = z.object({ ready: z.boolean() });

function pendingFetch() {
  return vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    const signal = init?.signal;
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    signal?.addEventListener("abort", () => reject(signal.reason), { once: true });
  }));
}

describe("getApiData", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("aborts a request that exceeds the timeout", async () => {
    vi.useFakeTimers();
    const fetchMock = pendingFetch();
    vi.stubGlobal("fetch", fetchMock);

    const request = getApiData("/health", responseSchema, "en");
    const result = expect(request).rejects.toMatchObject({ code: "TIMEOUT", status: 0 });
    await vi.advanceTimersByTimeAsync(API_REQUEST_TIMEOUT_MS);
    await result;

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("forwards cancellation and clears its timeout", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", pendingFetch());
    const controller = new AbortController();

    const request = getApiData("/health", responseSchema, "en", controller.signal);
    const result = expect(request).rejects.toMatchObject({ name: "AbortError" });
    controller.abort();
    await result;

    expect(vi.getTimerCount()).toBe(0);
  });
});
