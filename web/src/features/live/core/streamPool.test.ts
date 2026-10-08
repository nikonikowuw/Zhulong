import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FrontendStreamPool } from './streamPool';

class MockWebSocket {
  public static instances: MockWebSocket[] = [];
  public binaryType: string = 'blob';
  public onopen: (() => void) | null = null;
  public onmessage: ((event: { data: unknown }) => void) | null = null;
  public onerror: (() => void) | null = null;
  public onclose: (() => void) | null = null;
  public url: string;
  public closed = false;

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
    setTimeout(() => {
      if (this.onopen && !this.closed) {
        this.onopen();
      }
    }, 10);
  }

  close() {
    this.closed = true;
    if (this.onclose) {
      this.onclose();
    }
  }
}

describe('FrontendStreamPool', () => {
  let pool: FrontendStreamPool;
  const originalWebSocket = globalThis.WebSocket;

  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    // @ts-expect-error mock websocket
    globalThis.WebSocket = MockWebSocket;
    pool = new FrontendStreamPool();
  });

  afterEach(() => {
    pool.closeAll();
    globalThis.WebSocket = originalWebSocket;
    vi.useRealTimers();
  });

  it('multiplexes multiple subscribers onto a single WebSocket', () => {
    const fn1 = vi.fn();
    const fn2 = vi.fn();

    const unsub1 = pool.subscribe('cam-1', 'main', fn1);
    expect(MockWebSocket.instances.length).toBe(1);
    expect(pool.getRefCount('cam-1', 'main')).toBe(1);

    const unsub2 = pool.subscribe('cam-1', 'main', fn2);
    // Still single WS connection
    expect(MockWebSocket.instances.length).toBe(1);
    expect(pool.getRefCount('cam-1', 'main')).toBe(2);

    unsub1();
    expect(pool.getRefCount('cam-1', 'main')).toBe(1);
    expect(pool.hasGraceTimer('cam-1', 'main')).toBe(false);

    unsub2();
    expect(pool.getRefCount('cam-1', 'main')).toBe(0);
    expect(pool.hasGraceTimer('cam-1', 'main')).toBe(true);
  });

  it('delays WS disconnection by 3s grace period when refCount reaches 0', () => {
    const unsub = pool.subscribe('cam-1', 'main', vi.fn());
    expect(MockWebSocket.instances.length).toBe(1);
    const ws = MockWebSocket.instances[0]!;

    unsub();
    expect(pool.hasGraceTimer('cam-1', 'main')).toBe(true);
    expect(ws.closed).toBe(false);

    // Fast-forward 2 seconds - should still be alive
    vi.advanceTimersByTime(2000);
    expect(ws.closed).toBe(false);

    // Fast-forward 1.5 more seconds (total 3.5s) - should be closed
    vi.advanceTimersByTime(1500);
    expect(ws.closed).toBe(true);
    expect(pool.getRefCount('cam-1', 'main')).toBe(0);
  });

  it('cancels grace timer if a new subscriber attaches within 3s', () => {
    const unsub1 = pool.subscribe('cam-1', 'main', vi.fn());
    const ws = MockWebSocket.instances[0]!;

    unsub1();
    expect(pool.hasGraceTimer('cam-1', 'main')).toBe(true);

    // Advance 1.5s
    vi.advanceTimersByTime(1500);

    // Re-subscribe before grace timer expires
    const unsub2 = pool.subscribe('cam-1', 'main', vi.fn());
    expect(pool.hasGraceTimer('cam-1', 'main')).toBe(false);
    expect(pool.getRefCount('cam-1', 'main')).toBe(1);

    // Advance 3s more - WS must NOT be closed
    vi.advanceTimersByTime(3000);
    expect(ws.closed).toBe(false);

    unsub2();
  });

  it('notifies status listeners upon connection status change', () => {
    const statusFn = vi.fn();
    const unsub = pool.subscribe('cam-1', 'main', vi.fn(), statusFn);

    expect(statusFn).toHaveBeenCalledWith('connecting');

    vi.advanceTimersByTime(20);
    expect(statusFn).toHaveBeenCalledWith('connected');

    unsub();
  });
});
