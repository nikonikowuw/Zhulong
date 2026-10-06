import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { useLiveLayout } from './useLiveLayout';

describe('useLiveLayout', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('initializes with default mode 4 and empty slots', () => {
    const { result } = renderHook(() => useLiveLayout());
    expect(result.current.mode).toBe(4);
    expect(result.current.slots).toEqual({});
    expect(result.current.fullscreenSlot).toBeNull();
  });

  it('updates layout mode and resets fullscreen slot', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.setFullscreenSlot(1);
    });
    expect(result.current.fullscreenSlot).toBe(1);

    act(() => {
      result.current.setMode(9);
    });
    expect(result.current.mode).toBe(9);
    expect(result.current.fullscreenSlot).toBeNull();
  });

  it('assigns, toggles role, and clears slots', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.assignSlot(0, { cameraId: 'cam-1', role: 'main', name: 'Front Gate' });
    });
    expect(result.current.slots[0]).toEqual({
      cameraId: 'cam-1',
      role: 'main',
      name: 'Front Gate',
    });

    act(() => {
      result.current.toggleRole(0);
    });
    expect(result.current.slots[0]?.role).toBe('sub');

    act(() => {
      result.current.clearSlot(0);
    });
    expect(result.current.slots[0]).toBeUndefined();
  });

  it('persists layout state to localStorage', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.setMode(1);
      result.current.assignSlot(0, { cameraId: 'cam-2', role: 'main', name: 'Backyard' });
    });

    const saved = localStorage.getItem('zhulong_live_layout_v1');
    expect(saved).toBeTruthy();
    const parsed = JSON.parse(saved!);
    expect(parsed.mode).toBe(1);
    expect(parsed.slots[0]?.cameraId).toBe('cam-2');
  });
});
