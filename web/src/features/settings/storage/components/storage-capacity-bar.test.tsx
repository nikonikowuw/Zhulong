import {
  expectClassesApplied,
  expectDiffersInDarkMode,
  expectLevelHueFamily,
  expectPaletteRendersAs,
  expectSameComputedColor,
  LEVEL_HUE_FAMILY,
} from '@/test-utils/colors'
import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import {
  CATEGORY_NEUTRAL_SOLID_CLASS,
  CATEGORY_SOLID_CLASS,
} from '@/lib/category-palette'
import { STATUS_LEVEL_CLASS, type StatusLevel } from '@/lib/status-palette'
import { type StorageStatus } from '../types'
import { formatBytes } from '../utils/format'
import { StorageCapacityBar } from './storage-capacity-bar'

const mockStatus: StorageStatus = {
  mediaDirectory: '/mnt/storage/media',
  mountPoint: '/mnt/storage',
  fsType: 'ext4',
  totalBytes: 100 * 1024 * 1024 * 1024,
  usedBytes: 70 * 1024 * 1024 * 1024,
  freeBytes: 30 * 1024 * 1024 * 1024,
  usagePercent: 70,
  breakdown: {
    recordingsBytes: 50 * 1024 * 1024 * 1024,
    snapshotsBytes: 10 * 1024 * 1024 * 1024,
    exportsBytes: 5 * 1024 * 1024 * 1024,
    otherBytes: 5 * 1024 * 1024 * 1024,
  },
  status: 'healthy',
  deviceId: 2049,
  isExternal: true,
  canWrite: true,
  updatedAt: '2026-10-07T12:00:00Z',
}

describe('StorageCapacityBar', () => {
  it('formatBytes converts bytes into human-readable strings correctly', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB')
    expect(formatBytes(100 * 1024 * 1024 * 1024)).toBe('100.0 GB')
  })

  it('renders overview metrics and health badges properly', async () => {
    const { getByText } = await render(
      <StorageCapacityBar status={mockStatus} isLoading={false} />
    )

    await expect.element(getByText('100.0 GB')).toBeInTheDocument()
    await expect.element(getByText('30.0 GB')).toBeInTheDocument()
    await expect.element(getByText('运行正常')).toBeInTheDocument()
    await expect.element(getByText('独立外挂盘')).toBeInTheDocument()
    await expect.element(getByText('允许写盘')).toBeInTheDocument()
  })

  it('renders warning and stopped badges under critical conditions', async () => {
    const warningStatus: StorageStatus = {
      ...mockStatus,
      status: 'warning',
      usagePercent: 91,
    }
    const { getByText: getWarningText } = await render(
      <StorageCapacityBar status={warningStatus} />
    )
    await expect.element(getWarningText('高水位预警')).toBeInTheDocument()

    const stoppedStatus: StorageStatus = {
      ...mockStatus,
      status: 'emergency_stopped',
      canWrite: false,
    }
    const { getByText: getStoppedText } = await render(
      <StorageCapacityBar status={stoppedStatus} />
    )
    await expect.element(getStoppedText('停录熔断')).toBeInTheDocument()
    await expect.element(getStoppedText('禁止写入')).toBeInTheDocument()
  })

  /**
   * §1.3.6 堆叠条契约：图例圆点必须与对应分段**同色**。
   *
   * 期望值取自 `@/lib/category-palette`，并按分段的 `title`（含分类名）配对，
   * 不依赖 DOM 顺序。断言落到**实际渲染色**，因此「圆点写 blue-500、分段写
   * blue-600」这类漂移会被抓住，而断言两处类名相同则抓不到。
   */
  const LEGEND: Array<[string, string]> = [
    ['视频录像', CATEGORY_SOLID_CLASS.blue],
    ['AI抓拍', CATEGORY_SOLID_CLASS.violet],
    ['临时导出', CATEGORY_SOLID_CLASS.emerald],
    ['其它占用', CATEGORY_NEUTRAL_SOLID_CLASS],
  ]

  it('keeps every legend dot on the same §1.3.6 slot and colour as its bar segment', async () => {
    const { getByText, getByRole } = await render(
      <StorageCapacityBar status={mockStatus} isLoading={false} />
    )

    const bar = getByRole('progressbar').element() as HTMLElement
    const segments = [...bar.children] as HTMLElement[]
    expect(segments).toHaveLength(LEGEND.length)

    for (const [label, paletteClasses] of LEGEND) {
      // 分段按 title（形如「视频录像: 50.0 GB (50.0%)」）配对，不依赖顺序
      const segment = segments.find((s) =>
        s.getAttribute('title')?.startsWith(label)
      )
      expect(segment, `应存在标题以「${label}」开头的分段`).toBeDefined()

      // 图例圆点：label 前一个兄弟节点
      const dot = (getByText(label).element() as HTMLElement)
        .previousElementSibling as HTMLElement
      expect(dot).not.toBeNull()

      expectClassesApplied(dot, paletteClasses)
      await expectPaletteRendersAs(dot, paletteClasses)

      expectClassesApplied(segment!, paletteClasses)
      await expectPaletteRendersAs(segment!, paletteClasses)

      // 核心契约：两处实际渲染色必须相同
      expectSameComputedColor(
        dot,
        segment!,
        'backgroundColor',
        `图例圆点「${label}」应与对应分段同色`
      )
    }
  })

  const HEALTH_CASES: Array<[StorageStatus['status'], string, StatusLevel]> = [
    ['healthy', '运行正常', 'active'],
    ['warning', '高水位预警', 'warning'],
    ['emergency_stopped', '停录熔断', 'destructive'],
  ]

  it.each(HEALTH_CASES)(
    'renders the %s health badge on the %s §1.3.3 level',
    async (healthStatus, label, level) => {
      const { getByText } = await render(
        <StorageCapacityBar
          status={{
            ...mockStatus,
            status: healthStatus,
            canWrite: healthStatus !== 'emergency_stopped',
          }}
          isLoading={false}
        />
      )

      const badge = (getByText(label).element() as HTMLElement).closest(
        '[data-slot="badge"]'
      ) as HTMLElement
      expect(badge).not.toBeNull()

      expectClassesApplied(badge, STATUS_LEVEL_CLASS[level])
      await expectPaletteRendersAs(badge, STATUS_LEVEL_CLASS[level])
      await expectLevelHueFamily(
        STATUS_LEVEL_CLASS[level],
        LEVEL_HUE_FAMILY[level]
      )
      await expectDiffersInDarkMode(badge, 'borderTopColor')

      // §1.3.5：严禁实心 destructive 徽章（与同排浅色标签割裂）
      expect(badge.className).not.toContain('bg-destructive text-white')
    }
  )
})
