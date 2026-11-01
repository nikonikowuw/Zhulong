import { describe, expect, it } from 'vitest'
import { boxesCoincide, BOX_COINCIDENCE_EPSILON } from './box-geometry'

describe('boxesCoincide', () => {
  it('treats bit-identical boxes as coincident', () => {
    expect(boxesCoincide([0.1, 0.2, 0.3, 0.4], [0.1, 0.2, 0.3, 0.4])).toBe(true)
  })

  it('absorbs float32 → double serialization noise', () => {
    const serialized = 0.30000001192092896
    expect(
      boxesCoincide([0.1, 0.2, serialized, 0.4], [0.1, 0.2, 0.3, 0.4])
    ).toBe(true)
  })

  it('treats a real body box as distinct from the face box it contains', () => {
    expect(boxesCoincide([0.05, 0.05, 0.5, 0.9], [0.1, 0.1, 0.3, 0.3])).toBe(
      false
    )
  })

  it('honours a custom epsilon', () => {
    expect(boxesCoincide([0, 0, 0.01, 0.01], [0, 0, 0, 0], 0.001)).toBe(false)
    expect(boxesCoincide([0, 0, 0.01, 0.01], [0, 0, 0, 0], 0.02)).toBe(true)
  })

  it('exposes the contract-level default epsilon', () => {
    expect(BOX_COINCIDENCE_EPSILON).toBe(1e-4)
  })
})
