import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Gb28181Device } from '../data/schema'
import { Gb28181DeviceDeleteDialog } from './gb28181-device-delete-dialog'

const device: Gb28181Device = {
  deviceId: '34020000001320000001',
  name: 'East gate camera',
  manufacturer: null,
  model: null,
  firmware: null,
  transport: 'udp',
  remoteAddr: null,
  expiresSeconds: 3600,
  state: 'offline',
  channelCount: 2,
  lastKeepaliveAt: null,
  registeredAt: null,
  createdAt: '2026-10-25T07:00:00Z',
  updatedAt: '2026-10-25T07:00:00Z',
}

describe('Gb28181DeviceDeleteDialog', () => {
  it('shows the target device and confirms deletion', async () => {
    const onConfirm = vi.fn()
    const { getByRole, getByText } = await render(
      <Gb28181DeviceDeleteDialog
        open
        device={device}
        isPending={false}
        onOpenChange={vi.fn()}
        onConfirm={onConfirm}
      />
    )

    await expect
      .element(getByRole('heading', { name: '确认删除此设备？' }))
      .toBeInTheDocument()
    await expect.element(getByText(device.deviceId)).toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '删除设备' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('does not allow dismissing while deletion is pending', async () => {
    const onOpenChange = vi.fn()
    const { getByRole } = await render(
      <Gb28181DeviceDeleteDialog
        open
        device={device}
        isPending
        onOpenChange={onOpenChange}
        onConfirm={vi.fn()}
      />
    )

    await expect.element(getByRole('button', { name: '取消' })).toBeDisabled()
    expect(onOpenChange).not.toHaveBeenCalled()
  })
})
