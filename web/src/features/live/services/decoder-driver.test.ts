import { describe, it, expect, vi } from 'vitest'
import {
  createAdaptiveDecoderDriver,
  getOptimalDecoderMode,
  WasmDecoderDriver,
  WebCodecsDriver,
} from './decoder-driver'

describe('decoder-driver', () => {
  it('selects WebCodecsDriver when WebCodecs is supported', () => {
    const driver = createAdaptiveDecoderDriver({ onFrame: vi.fn() }, () => true)

    expect(driver).toBeInstanceOf(WebCodecsDriver)
    expect(driver.mode).toBe('WebCodecs')
    expect(getOptimalDecoderMode(() => true)).toBe('WebCodecs')
  })

  it('falls back to WasmDecoderDriver when WebCodecs is unsupported', () => {
    const driver = createAdaptiveDecoderDriver(
      { onFrame: vi.fn() },
      () => false
    )

    expect(driver).toBeInstanceOf(WasmDecoderDriver)
    expect(driver.mode).toBe('WASM')
    expect(getOptimalDecoderMode(() => false)).toBe('WASM')
  })
})
