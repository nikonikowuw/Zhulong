import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type SystemTimeStatus } from '../types'
import { TimeStatusBanner } from './time-status-banner'

const mockStatus: SystemTimeStatus = {
  currentTime: '2026-10-07T12:00:00Z',
  timezone: 'Asia/Shanghai',
  mode: 'ntp',
  ntpServers: ['ntp.aliyun.com'],
  syncIntervalSeconds: 900,
  syncStatus: {
    state: 'synchronized',
    lastSyncTime: '2026-10-07T11:45:00Z',
    lastSyncServer: 'ntp.aliyun.com',
    offsetMs: 12.3,
    rttMs: 25.1,
    errorMessage: '',
  },
  rtcStatus: 'normal',
  hasPermission: true,
}

describe('TimeStatusBanner', () => {
  it('renders timezone and sync status badges', async () => {
    const onSyncNow = vi.fn()
    const { getByText } = await render(
      <TimeStatusBanner
        status={mockStatus}
        isSyncing={false}
        onSyncNow={onSyncNow}
      />
    )

    await expect.element(getByText('Asia/Shanghai')).toBeInTheDocument()
    await expect.element(getByText('已同步')).toBeInTheDocument()
    await expect.element(getByText('RTC 就绪')).toBeInTheDocument()
  })

  it('renders permission alert when hasPermission is false', async () => {
    const restrictedStatus: SystemTimeStatus = {
      ...mockStatus,
      hasPermission: false,
    }

    const { getByText } = await render(
      <TimeStatusBanner
        status={restrictedStatus}
        isSyncing={false}
        onSyncNow={vi.fn()}
      />
    )

    await expect.element(getByText('系统权限受限')).toBeInTheDocument()
  })
})
