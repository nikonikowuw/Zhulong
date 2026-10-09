import { describe, it, expect, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { useLiveShortcuts } from './use-live-shortcuts'

describe('useLiveShortcuts', () => {
  it('按键 1、4、9 触发布局切换，按 Escape 触发重置最大化', async () => {
    const onLayoutChange = vi.fn()
    const onResetMaximize = vi.fn()

    await renderHook(() =>
      useLiveShortcuts({
        onLayoutChange,
        onResetMaximize,
      })
    )

    // 触发 '1'
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '1' }))
    expect(onLayoutChange).toHaveBeenCalledWith(1)

    // 触发 '4'
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '4' }))
    expect(onLayoutChange).toHaveBeenCalledWith(4)

    // 触发 '9'
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '9' }))
    expect(onLayoutChange).toHaveBeenCalledWith(9)

    // 触发 'Escape'
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(onResetMaximize).toHaveBeenCalled()
  })

  it('在输入框聚焦时不触发快捷键', async () => {
    const onLayoutChange = vi.fn()
    const onResetMaximize = vi.fn()

    await renderHook(() =>
      useLiveShortcuts({
        onLayoutChange,
        onResetMaximize,
      })
    )

    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: '4' }))
    expect(onLayoutChange).not.toHaveBeenCalled()

    document.body.removeChild(input)
  })
})
