import { describe, expect, it } from 'vitest'
import { algorithmLabel } from './schema'

describe('algorithmLabel', () => {
  it('prefers the snapshot name and appends the version', () => {
    expect(
      algorithmLabel({
        algorithmId: 'algo-1',
        algorithmName: '安全帽检测',
        algorithmVersion: '2.1.0',
      })
    ).toBe('安全帽检测 v2.1.0')
  })

  it('falls back to the snapshot name when the version is missing', () => {
    expect(algorithmLabel({ algorithmName: '安全帽检测' })).toBe('安全帽检测')
  })

  it('uses the snapshot even after the algorithm is uninstalled', () => {
    // 卸载后后端把 algorithmId 置空（外键 SET NULL），但快照仍在。
    // 这是本次改动的核心目的：历史证据不该失去来源信息。
    expect(
      algorithmLabel({
        algorithmId: '',
        algorithmName: '烟雾检测',
        algorithmVersion: '1.0.0',
      })
    ).toBe('烟雾检测 v1.0.0')
  })

  it('falls back to the raw id for rows predating the snapshot columns', () => {
    // 迁移 15 之前写入的历史行没有快照，只能显示 ID。
    expect(algorithmLabel({ algorithmId: 'algo-legacy' })).toBe('algo-legacy')
  })

  it('returns an empty string when nothing is known', () => {
    expect(algorithmLabel({})).toBe('')
    expect(algorithmLabel({ algorithmId: '', algorithmName: '', algorithmVersion: '' })).toBe('')
    // 空白的快照名不应被当成有效值（否则会显示 " v1.0.0"）
    expect(algorithmLabel({ algorithmName: '   ', algorithmVersion: '1.0.0' })).toBe('')
  })
})
