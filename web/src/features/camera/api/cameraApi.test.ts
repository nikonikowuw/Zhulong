import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listCameras,
  getCamera,
  createCamera,
  updateCamera,
  deleteCamera,
  diagnoseCamera,
  getCredentials,
} from "./cameraApi";

describe("cameraApi", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mockCamera = {
    id: "cam-1",
    name: "Front Gate",
    enabled: true,
    revision: 1,
    health: "online",
    session: "idle",
    degraded: false,
    stale: false,
    streams: [
      {
        id: 1,
        role: "main",
        protocol: "rtsp",
        rtspUrl: "rtsp://admin:pass@192.168.1.10/live",
        transport: "tcp",
        codec: "H.264",
        width: 1920,
        height: 1080,
        fps: 25,
        fpsString: "25/1",
        createdAt: "2026-10-06T00:00:00Z",
        updatedAt: "2026-10-06T00:00:00Z",
      },
    ],
    createdAt: "2026-10-06T00:00:00Z",
    updatedAt: "2026-10-06T00:00:00Z",
  };

  it("lists cameras", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: [mockCamera],
      }),
    } as Response);

    const result = await listCameras("zh-Hans");
    expect(result).toHaveLength(1);
    expect(result[0]?.name).toBe("Front Gate");
  });

  it("gets camera by id", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: mockCamera,
      }),
    } as Response);

    const result = await getCamera("cam-1", "zh-Hans");
    expect(result.id).toBe("cam-1");
    expect(result.name).toBe("Front Gate");
  });

  it("creates a camera", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: mockCamera,
      }),
    } as Response);

    const result = await createCamera(
      {
        name: "Front Gate",
        mainStream: {
          role: "main",
          rtspUrl: "rtsp://admin:pass@192.168.1.10/live",
          transport: "tcp",
        },
      },
      "zh-Hans",
    );
    expect(result.id).toBe("cam-1");
  });

  it("updates a camera", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: { ...mockCamera, revision: 2 },
      }),
    } as Response);

    const result = await updateCamera(
      "cam-1",
      {
        revision: 1,
        name: "Front Gate Updated",
      },
      "zh-Hans",
    );
    expect(result.revision).toBe(2);
  });

  it("deletes a camera", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: null,
      }),
    } as Response);

    const result = await deleteCamera("cam-1", "zh-Hans");
    expect(result).toBeNull();
  });

  it("diagnoses a camera", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          cameraId: "cam-1",
          message: "Probe succeeded",
          state: {
            cameraId: "cam-1",
            enabled: true,
            revision: 1,
            health: "online",
            session: "idle",
            degraded: false,
            stale: false,
            streams: {},
          },
        },
      }),
    } as Response);

    const result = await diagnoseCamera("cam-1", "zh-Hans");
    expect(result.cameraId).toBe("cam-1");
    expect(result.message).toBe("Probe succeeded");
  });

  it("fetches credentials", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        code: "OK",
        message: "success",
        data: {
          cameraId: "cam-1",
          credentials: {
            main: "rtsp://admin:pass@192.168.1.10/live",
          },
        },
      }),
    } as Response);

    const result = await getCredentials("cam-1", "zh-Hans");
    expect(result.credentials.main).toBe("rtsp://admin:pass@192.168.1.10/live");
  });
});
