import { useCallback, useEffect, useState } from 'react';
import type { LayoutMode, LiveLayoutState, SlotBinding } from '../core/types';

const STORAGE_KEY = 'zhulong_live_layout_v1';

const DEFAULT_STATE: LiveLayoutState = {
  mode: 4,
  slots: {},
};

export function useLiveLayout() {
  const [layoutState, setLayoutState] = useState<LiveLayoutState>(() => {
    if (typeof window === 'undefined') return DEFAULT_STATE;
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved) {
        const parsed = JSON.parse(saved);
        if ([1, 4, 9].includes(parsed.mode) && typeof parsed.slots === 'object') {
          return parsed as LiveLayoutState;
        }
      }
    } catch {
      // fallback
    }
    return DEFAULT_STATE;
  });

  const [fullscreenSlot, setFullscreenSlot] = useState<number | null>(null);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(layoutState));
    } catch {
      // ignore
    }
  }, [layoutState]);

  const setMode = useCallback((mode: LayoutMode) => {
    setLayoutState((prev) => ({ ...prev, mode }));
    setFullscreenSlot(null);
  }, []);

  const assignSlot = useCallback((slotIndex: number, binding: SlotBinding) => {
    setLayoutState((prev) => ({
      ...prev,
      slots: {
        ...prev.slots,
        [slotIndex]: binding,
      },
    }));
  }, []);

  const clearSlot = useCallback((slotIndex: number) => {
    setLayoutState((prev) => {
      const nextSlots = { ...prev.slots };
      delete nextSlots[slotIndex];
      return { ...prev, slots: nextSlots };
    });
  }, []);

  const toggleRole = useCallback((slotIndex: number) => {
    setLayoutState((prev) => {
      const current = prev.slots[slotIndex];
      if (!current) return prev;
      const nextRole = current.role === 'main' ? 'sub' : 'main';
      return {
        ...prev,
        slots: {
          ...prev.slots,
          [slotIndex]: {
            ...current,
            role: nextRole,
          },
        },
      };
    });
  }, []);

  return {
    mode: layoutState.mode,
    slots: layoutState.slots,
    fullscreenSlot,
    setMode,
    assignSlot,
    clearSlot,
    toggleRole,
    setFullscreenSlot,
  };
}
