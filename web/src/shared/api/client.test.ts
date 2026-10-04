import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { API_REQUEST_TIMEOUT_MS, getApiData, onUnauthorized, postApiData } from "./client";

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

describe("api client", () => {
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

  it("sends request with credentials: include and headers", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ code: "OK", message: "success", data: { ready: true } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const data = await getApiData("/health", responseSchema, "zh-Hans");
    expect(data).toEqual({ ready: true });
    expect(fetchMock).toHaveBeenCalledWith("/health", expect.objectContaining({
      credentials: "include",
      headers: expect.objectContaining({
        Accept: "application/json",
        "Accept-Language": "zh-Hans",
      }),
    }));
  });

  it("postApiData sends method POST with json body", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ code: "OK", message: "success", data: { ready: true } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const payload = { username: "admin" };
    const data = await postApiData("/login", payload, responseSchema, "en");
    expect(data).toEqual({ ready: true });
    expect(fetchMock).toHaveBeenCalledWith("/login", expect.objectContaining({
      method: "POST",
      credentials: "include",
      body: JSON.stringify(payload),
      headers: expect.objectContaining({
        "Content-Type": "application/json",
        Accept: "application/json",
        "Accept-Language": "en",
      }),
    }));
  });

  it("triggers onUnauthorized listener when response is 401", async () => {
    const unauthorizedCallback = vi.fn();
    const unsubscribe = onUnauthorized(unauthorizedCallback);

    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: () => Promise.resolve({ code: "UNAUTHORIZED", message: "Authentication required", data: null }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(getApiData("/protected", responseSchema, "en")).rejects.toMatchObject({
      status: 401,
      code: "UNAUTHORIZED",
    });

    expect(unauthorizedCallback).toHaveBeenCalledTimes(1);
    unsubscribe();

    // After unsubscribe, callback should not fire
    await expect(getApiData("/protected", responseSchema, "en")).rejects.toMatchObject({
      status: 401,
    });
    expect(unauthorizedCallback).toHaveBeenCalledTimes(1);
  });

  it("does not trigger onUnauthorized for public auth login endpoint", async () => {
    const unauthorizedCallback = vi.fn();
    const unsubscribe = onUnauthorized(unauthorizedCallback);

    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: () => Promise.resolve({ code: "INVALID_CREDENTIALS", message: "Invalid username or password", data: null }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(postApiData("/api/v1/auth/login", { username: "a", password: "b" }, responseSchema, "en")).rejects.toMatchObject({
      status: 401,
      code: "INVALID_CREDENTIALS",
    });

    expect(unauthorizedCallback).not.toHaveBeenCalled();
    unsubscribe();
  });
});
