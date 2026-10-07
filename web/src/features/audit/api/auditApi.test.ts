import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchAuditLogs, clearAuditLogs } from "./auditApi";

describe("auditApi", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mockItem = {
    id: 1,
    createdAt: "2026-10-06T12:00:00Z",
    ip: "192.168.1.1",
    username: "admin",
    action: "auth.login",
    target: "user:admin",
    detail: "{}",
    status: "success",
    errorMsg: "",
  };

  it("fetches paginated audit logs with query params", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          items: [mockItem],
          total: 1,
          page: 1,
          pageSize: 20,
        },
      }),
    } as Response);

    const result = await fetchAuditLogs(
      { page: 1, pageSize: 20, action: "auth.login", status: "success" },
      "en",
    );

    expect(result.items).toHaveLength(1);
    expect(result.items[0]?.action).toBe("auth.login");
    expect(result.total).toBe(1);
    expect(fetchSpy).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/audit/logs?page=1&pageSize=20&action=auth.login&status=success"),
      expect.anything(),
    );
  });

  it("clears audit logs", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          cleared: 15,
        },
      }),
    } as Response);

    const result = await clearAuditLogs("zh-Hans");
    expect(result.cleared).toBe(15);
    expect(fetchSpy).toHaveBeenCalledWith(
      "/api/v1/audit/logs",
      expect.objectContaining({ method: "DELETE" }),
    );
  });
});
