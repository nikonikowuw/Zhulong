import { useCallback, useEffect, useState } from 'react';
import type { LayoutMode, LiveLayoutState, SlotBinding } from '../core/types';

const STORAGE_KEY = 'zhulong_live_layout_v1';

const DEFAULT_STATE: LiveLayoutState = {
  mode: 4,
  slots: {},
  selectedSlotIndex: 0,
};

function loadSavedLayout(): LiveLayoutState {
  if (typeof window === 'undefined') return DEFAULT_STATE;

  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (!saved) return DEFAULT_STATE;

    const parsed = JSON.parse(saved);
    if ([1, 4, 9].includes(parsed.mode) && typeof parsed.slots === 'object') {
      return {
        mode: parsed.mode,
        slots: parsed.slots || {},
        selectedSlotIndex:
          typeof parsed.selectedSlotIndex === 'number' && parsed.selectedSlotIndex >= 0
            ? parsed.selectedSlotIndex
            : 0,
      };
    }
  } catch {
    // Fall back to default state
  }

  return DEFAULT_STATE;
}

export function useLiveLayout() {
  const [layoutState, setLayoutState] = useState<LiveLayoutState>(loadSavedLayout);
  const [fullscreenSlot, setFullscreenSlot] = useState<number | null>(null);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(layoutState));
    } catch {
      // Storage quota or privacy mode error; ignored
    }
  }, [layoutState]);

  const selectedSlotIndex =
    typeof layoutState.selectedSlotIndex === 'number' &&
    layoutState.selectedSlotIndex >= 0 &&
    (layoutState.mode === 1 || layoutState.selectedSlotIndex < layoutState.mode)
      ? layoutState.selectedSlotIndex
      : 0;

  const setMode = useCallback((mode: LayoutMode): void => {
    setLayoutState((prev) => {
      let nextSelected = prev.selectedSlotIndex ?? 0;
      if (mode > 1 && nextSelected >= mode) {
        nextSelected = mode - 1;
      }
      return { ...prev, mode, selectedSlotIndex: nextSelected };
    });
    setFullscreenSlot(null);
  }, []);

  const selectSlot = useCallback((slotIndex: number): void => {
    setLayoutState((prev) => {
      const maxIndex = prev.mode === 1 ? 8 : prev.mode - 1;
      const clamped = Math.max(0, Math.min(maxIndex, slotIndex));
      return { ...prev, selectedSlotIndex: clamped };
    });
  }, []);

  const assignSlot = useCallback((slotIndex: number, binding: SlotBinding): void => {
    setLayoutState((prev) => ({
      ...prev,
      slots: { ...prev.slots, [slotIndex]: binding },
      selectedSlotIndex: slotIndex,
    }));
  }, []);

  const assignToSelected = useCallback((binding: SlotBinding): void => {
    setLayoutState((prev) => {
      const capacity = prev.mode;
      const currentSelected = Math.max(
        0,
        prev.mode === 1
          ? (prev.selectedSlotIndex ?? 0)
          : Math.min(capacity - 1, prev.selectedSlotIndex ?? 0),
      );
      const targetSlot = currentSelected;
      const updatedSlots = { ...prev.slots, [targetSlot]: binding };

      // 1. 查找当前分屏容量下，下一个空闲槽位
      let nextEmptySlot: number | null = null;
      for (let i = 0; i < capacity; i++) {
        const candidate = (targetSlot + 1 + i) % capacity;
        if (!updatedSlots[candidate]) {
          nextEmptySlot = candidate;
          break;
        }
      }

      let nextSelected: number;
      if (nextEmptySlot !== null) {
        nextSelected = nextEmptySlot;
      } else {
        // 当前已无空闲视口：判断本次装载前是否曾经有空位
        let hadEmptyBefore = false;
        for (let i = 0; i < capacity; i++) {
          if (i !== targetSlot && !prev.slots[i]) {
            hadEmptyBefore = true;
            break;
          }
        }
        nextSelected = hadEmptyBefore ? 0 : (targetSlot + 1) % capacity;
      }

      return {
        ...prev,
        slots: updatedSlots,
        selectedSlotIndex: nextSelected,
      };
    });
  }, []);

  const clearSlot = useCallback((slotIndex: number): void => {
    setLayoutState((prev) => {
      const nextSlots = { ...prev.slots };
      delete nextSlots[slotIndex];
      return { ...prev, slots: nextSlots };
    });
  }, []);

  const clearAllSlots = useCallback((): void => {
    setLayoutState((prev) => ({
      ...prev,
      slots: {},
      selectedSlotIndex: 0,
    }));
    setFullscreenSlot(null);
  }, []);

  const toggleRole = useCallback((slotIndex: number): void => {
    setLayoutState((prev) => {
      const current = prev.slots[slotIndex];
      if (!current) return prev;
      const nextRole = current.role === 'main' ? 'sub' : 'main';
      return {
        ...prev,
        slots: {
          ...prev.slots,
          [slotIndex]: { ...current, role: nextRole },
        },
      };
    });
  }, []);

  const getPlayingSlot = useCallback(
    (cameraId: string, role?: 'main' | 'sub'): number | null => {
      for (let i = 0; i < 9; i++) {
        const binding = layoutState.slots[i];
        if (binding && binding.cameraId === cameraId) {
          if (!role || binding.role === role) {
            return i;
          }
        }
      }
      return null;
    },
    [layoutState.slots],
  );

  return {
    mode: layoutState.mode,
    slots: layoutState.slots,
    selectedSlotIndex,
    fullscreenSlot,
    setMode,
    selectSlot,
    assignSlot,
    assignToSelected,
    clearSlot,
    clearAllSlots,
    toggleRole,
    setFullscreenSlot,
    getPlayingSlot,
  };
}
