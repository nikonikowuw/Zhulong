import { describe, expect, it, vi } from "vitest";
import { formatFps, formatResolution, maskRtspUrl, copyToClipboard } from "./urlHelper";

describe("urlHelper", () => {
  describe("maskRtspUrl", () => {
    it("masks password in rtsp URL with username and password", () => {
      const url = "rtsp://admin:secret123@192.168.1.100:554/live/main";
      expect(maskRtspUrl(url)).toBe("rtsp://admin:••••••••@192.168.1.100:554/live/main");
    });

    it("masks password in rtsps URL", () => {
      const url = "rtsps://camera_user:my_pass_word@10.0.0.1:322/stream1";
      expect(maskRtspUrl(url)).toBe("rtsps://camera_user:••••••••@10.0.0.1:322/stream1");
    });

    it("leaves URL without password unchanged", () => {
      const url = "rtsp://192.168.1.100:554/live";
      expect(maskRtspUrl(url)).toBe("rtsp://192.168.1.100:554/live");
    });

    it("handles empty or falsy URL", () => {
      expect(maskRtspUrl("")).toBe("");
    });
  });

  describe("formatResolution", () => {
    it("formats known standard resolutions with label", () => {
      expect(formatResolution(3840, 2160)).toBe("3840×2160 (4K)");
      expect(formatResolution(2560, 1440)).toBe("2560×1440 (2K)");
      expect(formatResolution(1920, 1080)).toBe("1920×1080 (1080p)");
      expect(formatResolution(1280, 720)).toBe("1280×720 (720p)");
    });

    it("formats custom resolutions", () => {
      expect(formatResolution(800, 600)).toBe("800×600");
    });

    it("returns dash for zero or missing dimensions", () => {
      expect(formatResolution(0, 0)).toBe("-");
    });
  });

  describe("formatFps", () => {
    it("formats valid fps number", () => {
      expect(formatFps(25)).toBe("25 fps");
      expect(formatFps(29.97)).toBe("30 fps");
    });

    it("falls back to fpsString if fps number is missing", () => {
      expect(formatFps(0, "30/1")).toBe("30/1 fps");
    });

    it("returns dash when both are empty", () => {
      expect(formatFps(null, "")).toBe("-");
      expect(formatFps(undefined, "-")).toBe("-");
    });
  });

  describe("copyToClipboard", () => {
    it("calls navigator.clipboard.writeText when available", async () => {
      const writeTextMock = vi.fn().mockResolvedValue(undefined);
      Object.assign(navigator, {
        clipboard: {
          writeText: writeTextMock,
        },
      });

      const res = await copyToClipboard("test-text");
      expect(res).toBe(true);
      expect(writeTextMock).toHaveBeenCalledWith("test-text");
    });
  });
});
