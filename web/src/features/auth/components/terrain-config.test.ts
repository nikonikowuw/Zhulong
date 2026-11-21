import { describe, expect, it } from 'vitest'
import {
  buildAtmosphereMoteData,
  buildParticleFieldData,
  buildRidgeLineData,
  clampPixelRatio,
  resolveIsDarkMode,
  TERRAIN_PALETTES,
} from './terrain-config'

describe('terrain-config', () => {
  it('clampPixelRatio 将设备像素比限制在 [1, 2] 区间并处理异常值', () => {
    expect(clampPixelRatio(0)).toBe(1)
    expect(clampPixelRatio(-2)).toBe(1)
    expect(clampPixelRatio(Number.NaN)).toBe(1)
    expect(clampPixelRatio(1.25)).toBe(1.25)
    expect(clampPixelRatio(3)).toBe(2)
  })

  it('resolveIsDarkMode 优先读取根节点 dark / light 类名', () => {
    const darkEl = document.createElement('div')
    darkEl.classList.add('dark')
    expect(resolveIsDarkMode(darkEl)).toBe(true)

    const lightEl = document.createElement('div')
    lightEl.classList.add('light')
    expect(resolveIsDarkMode(lightEl)).toBe(false)
  })

  it('buildRidgeLineData 输出确定性、有限且尺寸匹配的线段缓冲', () => {
    const first = buildRidgeLineData(6, 4, 20, -10, 5)
    const second = buildRidgeLineData(6, 4, 20, -10, 5)

    // 4 行 × 6 段 × 2 端点 = 48 顶点
    expect(first.positions.length).toBe(48 * 3)
    expect(first.uvs.length).toBe(48 * 2)
    expect(first.positions).toEqual(second.positions)
    expect(first.positions.every((v) => Number.isFinite(v))).toBe(true)
  })

  it('buildParticleFieldData 生成带确定性扰动与归一化 UV 的粒子坐标', () => {
    const first = buildParticleFieldData(5, 4, 20, -10, 5)
    const second = buildParticleFieldData(5, 4, 20, -10, 5)

    expect(first.positions.length).toBe(20 * 3)
    expect(first.seeds.length).toBe(20 * 3)
    expect(first.uvs.length).toBe(20 * 2)
    expect(first.positions).toEqual(second.positions)
    expect(first.uvs.every((v) => v >= 0 && v <= 1)).toBe(true)
  })

  it('buildAtmosphereMoteData 生成确定数量且坐标有限的微尘粒子', () => {
    const motes = buildAtmosphereMoteData(32)
    expect(motes.positions.length).toBe(32 * 3)
    expect(motes.seeds.length).toBe(32 * 3)
    expect(motes.positions.every((v) => Number.isFinite(v))).toBe(true)
  })

  it('深浅两套色板透明度保持在有效区间', () => {
    for (const palette of [TERRAIN_PALETTES.light, TERRAIN_PALETTES.dark]) {
      expect(palette.lineOpacity).toBeGreaterThan(0)
      expect(palette.lineOpacity).toBeLessThanOrEqual(1)
      expect(palette.particleOpacity).toBeGreaterThan(0)
      expect(palette.particleOpacity).toBeLessThanOrEqual(1)
    }
  })
})
