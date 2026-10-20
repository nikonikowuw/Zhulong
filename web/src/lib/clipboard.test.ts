import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyToClipboard } from './clipboard'

/**
 * 覆写 `navigator.clipboard`。
 *
 * 传 `undefined` 用于模拟**非安全上下文**（边缘设备绑 `0.0.0.0` 后经明文 HTTP
 * 局域网访问）——这正是原实现会同步抛 TypeError 的场景。
 */
function stubClipboard(value: unknown): void {
  Object.defineProperty(navigator, 'clipboard', {
    value,
    configurable: true,
    writable: true,
  })
}

/** 覆写 `document.execCommand`，返回 spy 以便断言回退分支是否真的被走到。 */
function stubExecCommand(result: boolean | Error): ReturnType<typeof vi.fn> {
  const impl =
    result instanceof Error
      ? () => {
          throw result
        }
      : () => result
  const spy = vi.fn(impl)
  Object.defineProperty(document, 'execCommand', {
    value: spy,
    configurable: true,
    writable: true,
  })
  return spy
}

afterEach(() => {
  // 删除自身属性即可让原型上的原生 getter/方法重新生效
  delete (navigator as { clipboard?: unknown }).clipboard
  delete (document as { execCommand?: unknown }).execCommand
})

describe('copyToClipboard', () => {
  it('returns false for empty input without touching the clipboard', async () => {
    const writeText = vi.fn()
    stubClipboard({ writeText })

    await expect(copyToClipboard('')).resolves.toBe(false)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('prefers the async Clipboard API when it is available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    stubClipboard({ writeText })
    const execCommand = stubExecCommand(true)

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(true)

    expect(writeText).toHaveBeenCalledWith('helmet_detector')
    expect(execCommand).not.toHaveBeenCalled()
  })

  it('falls back to execCommand when the Clipboard API rejects', async () => {
    stubClipboard({ writeText: vi.fn().mockRejectedValue(new Error('denied')) })
    const execCommand = stubExecCommand(true)

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(true)

    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('returns false when both the Clipboard API and the fallback fail', async () => {
    stubClipboard({ writeText: vi.fn().mockRejectedValue(new Error('denied')) })
    stubExecCommand(false)

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(false)
  })

  it('does not throw when navigator.clipboard is unavailable (insecure context)', async () => {
    // 回归：这里正是裸调 navigator.clipboard.writeText 会同步抛 TypeError 的场景
    stubClipboard(undefined)
    const execCommand = stubExecCommand(true)

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(true)
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('removes the fallback textarea even when execCommand throws', async () => {
    stubClipboard(undefined)
    stubExecCommand(new Error('execCommand is not supported'))

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(false)
    expect(document.querySelectorAll('textarea')).toHaveLength(0)
  })

  it('removes the fallback textarea on the success path', async () => {
    stubClipboard(undefined)
    stubExecCommand(true)

    await expect(copyToClipboard('helmet_detector')).resolves.toBe(true)
    expect(document.querySelectorAll('textarea')).toHaveLength(0)
  })
})
