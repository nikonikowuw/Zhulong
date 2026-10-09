import { useEffect } from 'react'
import { type GridLayout } from '../types'

interface UseLiveShortcutsOptions {
  onLayoutChange: (layout: GridLayout) => void
  onResetMaximize: () => void
  enabled?: boolean
}

export function useLiveShortcuts({
  onLayoutChange,
  onResetMaximize,
  enabled = true,
}: UseLiveShortcutsOptions): void {
  useEffect(() => {
    if (!enabled) return

    const handleKeyDown = (e: KeyboardEvent) => {
      // 检查当前是否有聚焦的输入控件
      const activeEl = document.activeElement
      const isInput =
        activeEl instanceof HTMLInputElement ||
        activeEl instanceof HTMLTextAreaElement ||
        activeEl instanceof HTMLSelectElement ||
        activeEl?.getAttribute('contenteditable') === 'true'

      if (isInput) return

      // 检查当前是否有打开的模态框
      const hasOpenDialog = document.querySelector('[role="dialog"]') !== null
      if (hasOpenDialog) return

      switch (e.key) {
        case '1':
          onLayoutChange(1)
          break
        case '4':
          onLayoutChange(4)
          break
        case '9':
          onLayoutChange(9)
          break
        case 'Escape':
          onResetMaximize()
          break
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [enabled, onLayoutChange, onResetMaximize])
}
