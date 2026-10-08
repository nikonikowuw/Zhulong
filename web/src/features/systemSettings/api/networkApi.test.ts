import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fetchNetworkInterfaces,
  fetchNetworkStatus,
  applyNetworkConfig,
  confirmNetworkConfig,
  rollbackNetworkConfig,
  pingNetworkTarget,
} from "./networkApi";

describe("networkApi", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mockInterface = {
    name: "eth0",
    mac: "00:11:22:33:44:55",
    linkUp: true,
    mode: "dhcp",
    ipAddresses: ["192.168.1.100/24"],
    gateway: "192.168.1.1",
    dns: ["8.8.8.8"],
    isDefaultGw: true,
    isCurrent: true,
  };

  it("fetches network interfaces", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: [mockInterface],
      }),
    } as Response);

    const result = await fetchNetworkInterfaces("en");
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("eth0");
    expect(fetchSpy).toHaveBeenCalledWith(
      "/api/v1/system/network/interfaces",
      expect.anything(),
    );
  });

  it("fetches network interfaces with null or missing dns, gateway, and ipAddresses safely", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: [
          {
            name: "eth1",
            mac: "52:54:00:AB:CD:EF",
            linkUp: false,
            mode: "static",
            ipAddresses: null,
            gateway: null,
            dns: null,
            isDefaultGw: false,
            isCurrent: false,
          },
        ],
      }),
    } as Response);

    const result = await fetchNetworkInterfaces("en");
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("eth1");
    expect(result[0]?.dns).toEqual([]);
    expect(result[0]?.gateway).toBe("");
    expect(result[0]?.ipAddresses).toEqual([]);
  });

  it("fetches active network status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          transactionId: "tx-99",
          status: "pending_confirm",
          interfaceName: "eth0",
          confirmToken: "tok-99",
          targetUrl: "http://10.0.0.2:8080",
          timeoutSec: 60,
          expiresAt: "2026-10-06T12:00:00Z",
          rollbackConfig: {
            mode: "dhcp",
            ipAddress: "",
            subnetMask: "",
            gateway: "",
            dns: [],
            setDefault: false,
          },
        },
      }),
    } as Response);

    const result = await fetchNetworkStatus("en");
    expect(result?.transactionId).toBe("tx-99");
    expect(result?.status).toBe("pending_confirm");
  });

  it("applies network config", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          transactionId: "tx-1",
          timeoutSec: 60,
          targetUrl: "http://10.0.0.5:8080",
          confirmToken: "token-1",
        },
      }),
    } as Response);

    const result = await applyNetworkConfig(
      "eth0",
      {
        mode: "static",
        ipAddress: "10.0.0.5",
        subnetMask: "255.255.255.0",
        gateway: "10.0.0.1",
        dns: ["1.1.1.1"],
        setDefault: true,
      },
      "en",
    );

    expect(result.transactionId).toBe("tx-1");
    expect(result.confirmToken).toBe("token-1");
    expect(fetchSpy).toHaveBeenCalledWith(
      "/api/v1/system/network/interfaces/eth0/apply",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("confirms network config with token", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: { status: "confirmed" },
      }),
    } as Response);

    const result = await confirmNetworkConfig("test-token", "en");
    expect(result.status).toBe("confirmed");
    expect(fetchSpy).toHaveBeenCalledWith(
      "/api/v1/system/network/confirm?token=test-token",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("rolls back network config", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: { status: "rolled_back" },
      }),
    } as Response);

    const result = await rollbackNetworkConfig("test-token", "en");
    expect(result.status).toBe("rolled_back");
    expect(fetchSpy).toHaveBeenCalledWith(
      "/api/v1/system/network/rollback?token=test-token",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("pings network target", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: { reachable: true, rttMs: 12.5 },
      }),
    } as Response);

    const result = await pingNetworkTarget("8.8.8.8", "en");
    expect(result.reachable).toBe(true);
    expect(result.rttMs).toBe(12.5);
  });
});
