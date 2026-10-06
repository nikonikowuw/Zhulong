/**
 * Masks the password in an RTSP URL (e.g. rtsp://admin:123456@192.168.1.10:554/live -> rtsp://admin:••••••••@192.168.1.10:554/live).
 */
export function maskRtspUrl(rawUrl: string): string {
  if (!rawUrl) return "";
  // Regular expression matching scheme://user:pass@host...
  return rawUrl.replace(/^(rtsp[s]?:\/\/[^:]+):([^@]+)@/i, "$1:••••••••@");
}

/**
 * Copies text to the system clipboard, with fallback for environments without Clipboard API.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false;
  if (typeof navigator !== "undefined" && navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fallback below
    }
  }

  try {
    const textArea = document.createElement("textarea");
    textArea.value = text;
    textArea.style.position = "fixed";
    textArea.style.left = "-9999px";
    textArea.style.top = "0";
    textArea.setAttribute("readonly", "");
    document.body.appendChild(textArea);
    textArea.select();
    const successful = document.execCommand("copy");
    document.body.removeChild(textArea);
    return successful;
  } catch {
    return false;
  }
}

/**
 * Formats video resolution into readable string (e.g., "1920x1080 (1080p)").
 */
export function formatResolution(width: number, height: number): string {
  if (!width || !height) return "-";
  if (width === 3840 && height === 2160) return "3840×2160 (4K)";
  if (width === 2560 && height === 1440) return "2560×1440 (2K)";
  if (width === 1920 && height === 1080) return "1920×1080 (1080p)";
  if (width === 1280 && height === 720) return "1280×720 (720p)";
  return `${width}×${height}`;
}

/**
 * Formats FPS into readable string (e.g., "25 fps").
 */
export function formatFps(fps: number | null | undefined, fpsString?: string): string {
  if (fps && fps > 0) {
    return `${Math.round(fps * 10) / 10} fps`;
  }
  if (fpsString && fpsString !== "0/0" && fpsString !== "0/1" && fpsString !== "-") {
    return `${fpsString} fps`;
  }
  return "-";
}
