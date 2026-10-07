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
    expect(result.current.selectedSlotIndex).toBe(0);
    expect(result.current.fullscreenSlot).toBeNull();
  });

  it('updates layout mode and preserves selected slot in single view', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.selectSlot(3);
    });
    expect(result.current.selectedSlotIndex).toBe(3);

    act(() => {
      result.current.setMode(1);
    });
    expect(result.current.mode).toBe(1);
    // In mode 1, selectedSlotIndex is preserved to display slot 3 (视口 4)
    expect(result.current.selectedSlotIndex).toBe(3);

    act(() => {
      result.current.setMode(4);
    });
    expect(result.current.mode).toBe(4);
    expect(result.current.selectedSlotIndex).toBe(3);
  });

  it('assigns, toggles role, and clears slots', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.assignSlot(0, {
        cameraId: 'cam-1',
        role: 'main',
        name: 'Front Gate',
      });
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

  it('assignToSelected advances to next empty slot, resets to 0 when all slots filled', () => {
    const { result } = renderHook(() => useLiveLayout());

    expect(result.current.selectedSlotIndex).toBe(0);

    // 1. Assign to slot 0 -> advances to next empty slot 1
    act(() => {
      result.current.assignToSelected({
        cameraId: 'cam-1',
        role: 'main',
        name: 'Cam 1',
      });
    });
    expect(result.current.slots[0]?.cameraId).toBe('cam-1');
    expect(result.current.selectedSlotIndex).toBe(1);

    // 2. Assign to slot 1 -> advances to next empty slot 2
    act(() => {
      result.current.assignToSelected({
        cameraId: 'cam-2',
        role: 'main',
        name: 'Cam 2',
      });
    });
    expect(result.current.slots[1]?.cameraId).toBe('cam-2');
    expect(result.current.selectedSlotIndex).toBe(2);

    // 3. Assign to slot 2 -> advances to next empty slot 3
    act(() => {
      result.current.assignToSelected({
        cameraId: 'cam-3',
        role: 'main',
        name: 'Cam 3',
      });
    });
    expect(result.current.slots[2]?.cameraId).toBe('cam-3');
    expect(result.current.selectedSlotIndex).toBe(3);

    // 4. Assign to slot 3 -> ALL 4 slots are now filled (满屏) -> resets to 0!
    act(() => {
      result.current.assignToSelected({
        cameraId: 'cam-4',
        role: 'main',
        name: 'Cam 4',
      });
    });
    expect(result.current.slots[3]?.cameraId).toBe('cam-4');
    expect(result.current.selectedSlotIndex).toBe(0);

    // 5. When already full, replacing slot 0 rolls forward to slot 1
    act(() => {
      result.current.assignToSelected({
        cameraId: 'cam-5',
        role: 'sub',
        name: 'Cam 5',
      });
    });
    expect(result.current.slots[0]?.cameraId).toBe('cam-5');
    expect(result.current.selectedSlotIndex).toBe(1);
  });

  it('clears all slots with clearAllSlots', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.assignSlot(0, { cameraId: 'cam-1', role: 'main' });
      result.current.assignSlot(1, { cameraId: 'cam-2', role: 'sub' });
      result.current.selectSlot(2);
    });

    expect(Object.keys(result.current.slots).length).toBe(2);

    act(() => {
      result.current.clearAllSlots();
    });

    expect(result.current.slots).toEqual({});
    expect(result.current.selectedSlotIndex).toBe(0);
  });

  it('identifies playing slots via getPlayingSlot even across mode changes', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.assignSlot(2, {
        cameraId: 'cam-xyz',
        role: 'main',
        name: 'Gate',
      });
    });

    // In mode 4
    expect(result.current.getPlayingSlot('cam-xyz')).toBe(2);

    // Switch to mode 1: still returns slot 2
    act(() => {
      result.current.setMode(1);
    });
    expect(result.current.getPlayingSlot('cam-xyz')).toBe(2);
    expect(result.current.getPlayingSlot('cam-xyz', 'main')).toBe(2);
    expect(result.current.getPlayingSlot('cam-xyz', 'sub')).toBeNull();
    expect(result.current.getPlayingSlot('non-existent')).toBeNull();
  });

  it('persists layout state to localStorage', () => {
    const { result } = renderHook(() => useLiveLayout());

    act(() => {
      result.current.setMode(1);
      result.current.assignSlot(0, {
        cameraId: 'cam-2',
        role: 'main',
        name: 'Backyard',
      });
    });

    const saved = localStorage.getItem('zhulong_live_layout_v1');
    expect(saved).toBeTruthy();
    const parsed = JSON.parse(saved!);
    expect(parsed.mode).toBe(1);
    expect(parsed.slots[0]?.cameraId).toBe('cam-2');
  });
});
