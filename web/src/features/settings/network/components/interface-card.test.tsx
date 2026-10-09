import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type InterfaceInfo } from '../api/network-api'
import { InterfaceCard } from './interface-card'

const mockInterface: InterfaceInfo = {
  name: 'eth0',
  mac: '52:54:00:12:34:56',
  linkUp: true,
  mode: 'static',
  ipAddresses: ['192.168.1.100/24'],
  gateway: '192.168.1.1',
  dns: ['8.8.8.8', '114.114.114.114'],
  isDefaultGw: true,
  isCurrent: true,
}

describe('InterfaceCard', () => {
  it('renders network interface metrics and status badges', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByText } = await render(
      <InterfaceCard iface={mockInterface} onEdit={onEdit} onPing={onPing} />
    )

    await expect.element(getByText('eth0')).toBeInTheDocument()
    await expect
      .element(getByText('MAC: 52:54:00:12:34:56'))
      .toBeInTheDocument()
    await expect.element(getByText('已连接 (Link Up)')).toBeInTheDocument()
    await expect.element(getByText('192.168.1.100/24')).toBeInTheDocument()
    await expect
      .element(getByText('192.168.1.1', { exact: true }))
      .toBeInTheDocument()
    await expect.element(getByText('当前管理连接')).toBeInTheDocument()
    await expect
      .element(getByText('8.8.8.8, 114.114.114.114'))
      .toBeInTheDocument()
  })

  it('triggers onEdit callback when configure button is clicked', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByRole } = await render(
      <InterfaceCard iface={mockInterface} onEdit={onEdit} onPing={onPing} />
    )

    await userEvent.click(getByRole('button', { name: /配置网卡/i }))
    expect(onEdit).toHaveBeenCalledWith(mockInterface)
  })

  it('triggers onPing callback when probe button is clicked', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByRole } = await render(
      <InterfaceCard iface={mockInterface} onEdit={onEdit} onPing={onPing} />
    )

    await userEvent.click(getByRole('button', { name: /探测连通性/i }))
    expect(onPing).toHaveBeenCalledWith('192.168.1.1')
  })
})
