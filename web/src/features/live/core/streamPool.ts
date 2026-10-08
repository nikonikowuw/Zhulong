import type { ConnectionStatus, ParsedPacket } from './types';
import { parseZlm1Packet } from './wireParser';

export type PacketCallback = (packet: ParsedPacket) => void;
export type StatusCallback = (status: ConnectionStatus) => void;

export interface StreamEntry {
  cameraId: string;
  role: 'main' | 'sub';
  ws: WebSocket | null;
  refCount: number;
  status: ConnectionStatus;
  packetListeners: Set<PacketCallback>;
  statusListeners: Set<StatusCallback>;
  graceTimer: ReturnType<typeof setTimeout> | null;
  reconnectTimer: ReturnType<typeof setTimeout> | null;
  reconnectAttempts: number;
}

export const GRACE_DISCONNECT_MS = 3000;
export const MAX_RECONNECT_DELAY_MS = 15000;
export const BASE_RECONNECT_DELAY_MS = 1000;

export class FrontendStreamPool {
  private entries = new Map<string, StreamEntry>();

  private getStreamKey(cameraId: string, role: 'main' | 'sub'): string {
    return `${cameraId}:${role}`;
  }

  private buildWsUrl(cameraId: string, role: 'main' | 'sub'): string {
    const isHttps = typeof window !== 'undefined' && window.location?.protocol === 'https:';
    const host = typeof window !== 'undefined' && window.location?.host ? window.location.host : '127.0.0.1:8080';
    const proto = isHttps ? 'wss:' : 'ws:';
    return `${proto}//${host}/api/v1/cameras/${encodeURIComponent(cameraId)}/streams/${encodeURIComponent(role)}/ws`;
  }

  /**
   * 订阅指定摄像机与码流。
   * 返回取消订阅函数 (unsubscribe)。
   */
  public subscribe(
    cameraId: string,
    role: 'main' | 'sub',
    onPacket: PacketCallback,
    onStatus?: StatusCallback,
  ): () => void {
    const key = this.getStreamKey(cameraId, role);
    let entry = this.entries.get(key);

    if (entry) {
      if (entry.graceTimer) {
        clearTimeout(entry.graceTimer);
        entry.graceTimer = null;
      }
      entry.refCount++;
      entry.packetListeners.add(onPacket);
      if (onStatus) {
        entry.statusListeners.add(onStatus);
        onStatus(entry.status);
      }
    } else {
      entry = {
        cameraId,
        role,
        ws: null,
        refCount: 1,
        status: 'connecting',
        packetListeners: new Set([onPacket]),
        statusListeners: new Set(onStatus ? [onStatus] : []),
        graceTimer: null,
        reconnectTimer: null,
        reconnectAttempts: 0,
      };
      this.entries.set(key, entry);
      if (onStatus) {
        onStatus(entry.status);
      }
      this.connect(entry);
    }

    return () => {
      this.unsubscribe(cameraId, role, onPacket, onStatus);
    };
  }

  /**
   * 取消订阅并进入 3 秒防抖延迟释放逻辑。
   */
  public unsubscribe(
    cameraId: string,
    role: 'main' | 'sub',
    onPacket: PacketCallback,
    onStatus?: StatusCallback,
  ): void {
    const key = this.getStreamKey(cameraId, role);
    const entry = this.entries.get(key);
    if (!entry) return;

    entry.packetListeners.delete(onPacket);
    if (onStatus) {
      entry.statusListeners.delete(onStatus);
    }
    entry.refCount = Math.max(0, entry.refCount - 1);

    if (entry.refCount === 0) {
      if (entry.graceTimer) {
        clearTimeout(entry.graceTimer);
      }
      entry.graceTimer = setTimeout(() => {
        this.destroyEntry(key);
      }, GRACE_DISCONNECT_MS);
    }
  }

  private connect(entry: StreamEntry): void {
    if (typeof WebSocket === 'undefined') {
      entry.status = 'error';
      this.notifyStatus(entry);
      return;
    }

    try {
      const url = this.buildWsUrl(entry.cameraId, entry.role);
      const ws = new WebSocket(url);
      ws.binaryType = 'arraybuffer';
      entry.ws = ws;

      ws.onopen = () => {
        entry.status = 'connected';
        entry.reconnectAttempts = 0;
        this.notifyStatus(entry);
      };

      ws.onmessage = (event: MessageEvent) => {
        if (event.data instanceof ArrayBuffer) {
          try {
            const packet = parseZlm1Packet(event.data);
            for (const listener of entry.packetListeners) {
              listener(packet);
            }
          } catch {
            // 忽略格式异常包
          }
        }
      };

      ws.onerror = () => {
        entry.status = 'error';
        this.notifyStatus(entry);
      };

      ws.onclose = () => {
        entry.ws = null;
        if (entry.refCount > 0) {
          entry.status = 'reconnecting';
          this.notifyStatus(entry);
          this.scheduleReconnect(entry);
        } else {
          entry.status = 'disconnected';
          this.notifyStatus(entry);
        }
      };
    } catch {
      entry.status = 'error';
      this.notifyStatus(entry);
    }
  }

  private scheduleReconnect(entry: StreamEntry): void {
    if (entry.reconnectTimer) {
      clearTimeout(entry.reconnectTimer);
    }
    const delay = Math.min(
      BASE_RECONNECT_DELAY_MS * Math.pow(2, entry.reconnectAttempts),
      MAX_RECONNECT_DELAY_MS,
    );
    entry.reconnectAttempts++;

    entry.reconnectTimer = setTimeout(() => {
      entry.reconnectTimer = null;
      if (entry.refCount > 0) {
        this.connect(entry);
      }
    }, delay);
  }

  private destroyEntry(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) return;

    if (entry.reconnectTimer) {
      clearTimeout(entry.reconnectTimer);
      entry.reconnectTimer = null;
    }
    if (entry.graceTimer) {
      clearTimeout(entry.graceTimer);
      entry.graceTimer = null;
    }
    if (entry.ws) {
      entry.ws.onopen = null;
      entry.ws.onmessage = null;
      entry.ws.onerror = null;
      entry.ws.onclose = null;
      entry.ws.close();
      entry.ws = null;
    }
    entry.status = 'disconnected';
    this.notifyStatus(entry);
    this.entries.delete(key);
  }

  private notifyStatus(entry: StreamEntry): void {
    for (const listener of entry.statusListeners) {
      listener(entry.status);
    }
  }

  // --- 调试与测试辅助方法 ---
  public getRefCount(cameraId: string, role: 'main' | 'sub'): number {
    return this.entries.get(this.getStreamKey(cameraId, role))?.refCount ?? 0;
  }

  public getStatus(cameraId: string, role: 'main' | 'sub'): ConnectionStatus | undefined {
    return this.entries.get(this.getStreamKey(cameraId, role))?.status;
  }

  public hasGraceTimer(cameraId: string, role: 'main' | 'sub'): boolean {
    return this.entries.get(this.getStreamKey(cameraId, role))?.graceTimer !== null;
  }

  public closeAll(): void {
    for (const key of Array.from(this.entries.keys())) {
      this.destroyEntry(key);
    }
  }
}

export const streamPool = new FrontendStreamPool();
