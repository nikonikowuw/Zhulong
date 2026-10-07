import { useCallback, useEffect, useState } from 'react';
import type { LayoutMode, LiveLayoutState, SlotBinding } from '../core/types';

const STORAGE_KEY = 'zhulong_live_layout_v1';

const DEFAULT_STATE: LiveLayoutState = {
  mode: 4,
  slots: {},
  selectedSlotIndex: 0,
};

export function useLiveLayout() {
  const [layoutState, setLayoutState] = useState<LiveLayoutState>(() => {
    if (typeof window === 'undefined') return DEFAULT_STATE;
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved) {
        const parsed = JSON.parse(saved);
        if ([1, 4, 9].includes(parsed.mode) && typeof parsed.slots === 'object') {
          return {
            mode: parsed.mode,
            slots: parsed.slots,
            selectedSlotIndex:
              typeof parsed.selectedSlotIndex === 'number' &&
              parsed.selectedSlotIndex >= 0
                ? parsed.selectedSlotIndex
                : 0,
          } as LiveLayoutState;
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

  const selectedSlotIndex =
    typeof layoutState.selectedSlotIndex === 'number' &&
    layoutState.selectedSlotIndex >= 0 &&
    (layoutState.mode === 1 || layoutState.selectedSlotIndex < layoutState.mode)
      ? layoutState.selectedSlotIndex
      : 0;

  const setMode = useCallback((mode: LayoutMode) => {
    setLayoutState((prev) => {
      let nextSelected = prev.selectedSlotIndex ?? 0;
      // 多分屏模式且超出当前 mode 范围时，夹紧到当前模式最后一个槽位
      if (mode > 1 && nextSelected >= mode) {
        nextSelected = mode - 1;
      }
      return { ...prev, mode, selectedSlotIndex: nextSelected };
    });
    setFullscreenSlot(null);
  }, []);

  const selectSlot = useCallback((slotIndex: number) => {
    setLayoutState((prev) => {
      const maxIndex = prev.mode === 1 ? 8 : prev.mode - 1;
      const clamped = Math.max(0, Math.min(maxIndex, slotIndex));
      return { ...prev, selectedSlotIndex: clamped };
    });
  }, []);

  const assignSlot = useCallback((slotIndex: number, binding: SlotBinding) => {
    setLayoutState((prev) => ({
      ...prev,
      slots: {
        ...prev.slots,
        [slotIndex]: binding,
      },
      selectedSlotIndex: slotIndex,
    }));
  }, []);

  const assignToSelected = useCallback((binding: SlotBinding) => {
    setLayoutState((prev) => {
      const capacity = prev.mode;
      const currentSelected = Math.max(
        0,
        prev.mode === 1
          ? (prev.selectedSlotIndex ?? 0)
          : Math.min(capacity - 1, prev.selectedSlotIndex ?? 0),
      );
      const targetSlot = currentSelected;
      const updatedSlots = {
        ...prev.slots,
        [targetSlot]: binding,
      };

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
        // 还有空视口：自动顺延至下一个空视口
        nextSelected = nextEmptySlot;
      } else {
        // 当前分屏已无空闲视口：
        // 检查本次装载前是否还有空槽位（即本次操作是否刚好填满了整个分屏）
        const hadEmptyBefore = Array.from(
          { length: capacity },
          (_, i) => i,
        ).some((i) => i !== targetSlot && !prev.slots[i]);

        if (hadEmptyBefore) {
          // 满屏后，重新从 0 开始
          nextSelected = 0;
        } else {
          // 已处于全满状态：顺延覆盖下一个视口 (0 -> 1 -> 2 ... -> 0)
          nextSelected = (targetSlot + 1) % capacity;
        }
      }

      return {
        ...prev,
        slots: updatedSlots,
        selectedSlotIndex: nextSelected,
      };
    });
  }, []);

  const clearSlot = useCallback((slotIndex: number) => {
    setLayoutState((prev) => {
      const nextSlots = { ...prev.slots };
      delete nextSlots[slotIndex];
      return { ...prev, slots: nextSlots };
    });
  }, []);

  const clearAllSlots = useCallback(() => {
    setLayoutState((prev) => ({
      ...prev,
      slots: {},
      selectedSlotIndex: 0,
    }));
    setFullscreenSlot(null);
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

  const getPlayingSlot = useCallback(
    (cameraId: string, role?: 'main' | 'sub'): number | null => {
      // 检查系统中所有已配置的槽位（最大 9 分屏），切换分屏后依然按原始 slot 号显示
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
