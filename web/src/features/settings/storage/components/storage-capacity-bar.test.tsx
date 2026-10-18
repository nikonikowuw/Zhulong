import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
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

  it('keeps every legend dot on the same §1.3.6 slot as its bar segment', async () => {
    const { getByText } = await render(
      <StorageCapacityBar status={mockStatus} isLoading={false} />
    )

    const labels: Array<[string, string]> = [
      ['视频录像', 'bg-blue-500'],
      ['AI抓拍', 'bg-violet-500'],
      ['临时导出', 'bg-emerald-500'],
      ['其它占用', 'bg-muted-foreground/60'],
    ]

    for (const [label, expectedSolid] of labels) {
      // 图例圆点：label 前一个兄弟节点
      const dot = (getByText(label).element() as HTMLElement)
        .previousElementSibling
      expect(dot).not.toBeNull()
      expect((dot as HTMLElement).className).toContain(expectedSolid)
    }
  })

  it.each([
    ['healthy', '运行正常', 'border-teal-200', 'dark:border-teal-800'],
    ['warning', '高水位预警', 'border-amber-200', 'dark:border-amber-800'],
    [
      'emergency_stopped',
      '停录熔断',
      'border-destructive/20',
      'dark:border-destructive/40',
    ],
  ] as const)(
    'renders the %s health badge with the paired §1.3.3 level classes',
    async (healthStatus, label, lightBorder, darkBorder) => {
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
      )
      expect(badge).not.toBeNull()
      const classes = (badge as HTMLElement).className

      expect(classes).toContain(lightBorder)
      expect(classes).toContain(darkBorder)
      // §1.3.5：严禁实心 destructive 徽章（与同排浅色标签割裂）
      expect(classes).not.toContain('bg-destructive text-white')
    }
  )
})
