import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Gb28181Device } from '../data/schema'
import { Gb28181DevicesTable } from './gb28181-devices-table'

const onlineDevice: Gb28181Device = {
  deviceId: '34020000001320000001',
  name: '东门球机',
  manufacturer: null,
  model: null,
  firmware: null,
  transport: 'udp',
  remoteAddr: '192.168.1.64:5060',
  expiresSeconds: 3600,
  state: 'online',
  lastKeepaliveAt: '2026-10-25T08:00:00Z',
  registeredAt: '2026-10-25T07:00:00Z',
  createdAt: '2026-10-25T07:00:00Z',
  updatedAt: '2026-10-25T08:00:00Z',
}

const offlineDevice: Gb28181Device = {
  ...onlineDevice,
  deviceId: '34020000001320000002',
  name: '',
  transport: 'tcp',
  remoteAddr: null,
  state: 'offline',
  lastKeepaliveAt: null,
}

function renderTable(
  overrides: Partial<React.ComponentProps<typeof Gb28181DevicesTable>> = {}
) {
  return render(
    <Gb28181DevicesTable
      devices={[onlineDevice, offlineDevice]}
      isLoading={false}
      isError={false}
      onRetry={vi.fn()}
      onEdit={vi.fn()}
      search={{}}
      navigate={vi.fn()}
      {...overrides}
    />
  )
}

describe('Gb28181DevicesTable', () => {
  it('renders device identity, state and lease', async () => {
    const { getByText } = await renderTable()

    await expect.element(getByText('34020000001320000001')).toBeInTheDocument()
    await expect.element(getByText('东门球机')).toBeInTheDocument()
    await expect.element(getByText('在线', { exact: true })).toBeInTheDocument()
    await expect.element(getByText('离线', { exact: true })).toBeInTheDocument()
    await expect.element(getByText('192.168.1.64:5060')).toBeInTheDocument()
    // 未命名的设备不会渲染成空白单元格
    await expect.element(getByText('34020000001320000002')).toBeInTheDocument()
  })

  it('hands the selected device to the credential editor', async () => {
    const onEdit = vi.fn()
    const { getByRole } = await renderTable({ onEdit })

    await userEvent.click(getByRole('button', { name: '更新凭据' }).first())

    expect(onEdit).toHaveBeenCalledWith(onlineDevice)
  })

  it('guides the operator when the allowlist is empty', async () => {
    const { getByText, container } = await renderTable({ devices: [] })

    await expect.element(getByText('尚未预置任何设备')).toBeInTheDocument()
    await expect
      .element(getByText(/预置其国标编号与 Digest 密码/))
      .toBeInTheDocument()
    // 0 行不留分页条：否则会出现「第 1 页，共 0 页」（theme-and-i18n.md §4.1）。
    expect(container.textContent).not.toContain('共 0 页')
  })

  it('offers a retry when the allowlist cannot be read', async () => {
    const onRetry = vi.fn()
    const { getByRole, getByText } = await renderTable({
      isError: true,
      errorMessage: '数据库操作失败',
      onRetry,
    })

    await expect.element(getByText('读取设备白名单失败')).toBeInTheDocument()
    await expect.element(getByText('数据库操作失败')).toBeInTheDocument()

    await userEvent.click(getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('filters devices by state with multiple selections in faceted filter', async () => {
    const { getByText } = await renderTable({
      search: { status: ['offline'] },
    })

    await expect.element(getByText('34020000001320000002')).toBeInTheDocument()
    await expect
      .element(getByText('34020000001320000001'))
      .not.toBeInTheDocument()
  })

  it('keeps matching rows when both online and offline are selected', async () => {
    const { getByText } = await renderTable({
      search: { status: ['online', 'offline'] },
    })

    await expect.element(getByText('34020000001320000001')).toBeInTheDocument()
    await expect.element(getByText('34020000001320000002')).toBeInTheDocument()
  })
})
