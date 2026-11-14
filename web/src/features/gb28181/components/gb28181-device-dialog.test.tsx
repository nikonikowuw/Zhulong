import { toast } from 'sonner'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Gb28181Device } from '../data/schema'
import { Gb28181DeviceDialog } from './gb28181-device-dialog'

const existingDevice: Gb28181Device = {
  deviceId: '34020000001320000001',
  name: '东门球机',
  manufacturer: null,
  model: null,
  firmware: null,
  transport: 'udp',
  remoteAddr: '192.168.1.64:5060',
  expiresSeconds: 3600,
  state: 'online',
  channelCount: 2,
  lastKeepaliveAt: '2026-10-25T08:00:00Z',
  registeredAt: '2026-10-25T07:00:00Z',
  createdAt: '2026-10-25T07:00:00Z',
  updatedAt: '2026-10-25T08:00:00Z',
}

describe('Gb28181DeviceDialog', () => {
  it('requires password when provisioning a new device', async () => {
    const onSubmit = vi.fn()
    const { getByLabelText, getByRole, getByText } = await render(
      <Gb28181DeviceDialog
        open={true}
        device={null}
        isPending={false}
        onOpenChange={vi.fn()}
        onSubmit={onSubmit}
      />
    )

    await userEvent.fill(getByLabelText('设备编号'), '34020000001320000002')
    await userEvent.fill(getByLabelText('注册有效期（秒）'), '3600')
    await userEvent.click(getByRole('button', { name: '保存配置' }))

    expect(onSubmit).not.toHaveBeenCalled()
    await expect
      .element(getByText('新预置设备必须提供 Digest 密码'))
      .toBeInTheDocument()
  })

  it('omits password when left empty in edit mode and preserves updated name', async () => {
    const onSubmit = vi.fn().mockResolvedValueOnce(undefined)
    const onOpenChange = vi.fn()

    const { getByLabelText, getByRole } = await render(
      <Gb28181DeviceDialog
        open={true}
        device={existingDevice}
        isPending={false}
        onOpenChange={onOpenChange}
        onSubmit={onSubmit}
      />
    )

    const nameInput = getByLabelText('设备名称')
    await userEvent.clear(nameInput)
    await userEvent.fill(nameInput, '西门枪机')
    await userEvent.click(getByRole('button', { name: '保存配置' }))

    await vi.waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith('34020000001320000001', {
        name: '西门枪机',
        password: undefined,
        transport: 'udp',
        expiresSeconds: 3600,
      })
    })
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('shows an error toast when onSubmit rejects with a non-Axios error', async () => {
    const errorSpy = vi.spyOn(toast, 'error').mockImplementation(() => '')
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error('unexpected device failure'))

    const { getByRole } = await render(
      <Gb28181DeviceDialog
        open={true}
        device={existingDevice}
        isPending={false}
        onOpenChange={vi.fn()}
        onSubmit={onSubmit}
      />
    )

    await userEvent.click(getByRole('button', { name: '保存配置' }))

    await vi.waitFor(() => {
      expect(errorSpy).toHaveBeenCalledWith('unexpected device failure')
    })
    errorSpy.mockRestore()
  })
})
