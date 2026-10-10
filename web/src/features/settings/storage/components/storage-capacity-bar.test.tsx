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
})
