import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type InterfaceInfo } from '../api/network-api'
import { InterfaceTable } from './interface-table'

const mockInterfaces: InterfaceInfo[] = [
  {
    name: 'eth0',
    mac: '52:54:00:12:34:56',
    linkUp: true,
    mode: 'static',
    ipAddresses: ['192.168.1.100/24'],
    gateway: '192.168.1.1',
    dns: ['8.8.8.8'],
    isDefaultGw: true,
    isCurrent: true,
  },
  {
    name: 'eth1',
    mac: '52:54:00:12:34:57',
    linkUp: false,
    mode: 'dhcp',
    ipAddresses: [],
    gateway: '',
    dns: [],
    isDefaultGw: false,
    isCurrent: false,
  },
]

describe('InterfaceTable', () => {
  it('renders table columns and interface rows', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByText } = await render(
      <InterfaceTable
        interfaces={mockInterfaces}
        isLoading={false}
        isError={false}
        error={null}
        onRefetch={vi.fn()}
        onEdit={onEdit}
        onPing={onPing}
      />
    )

    await expect.element(getByText('eth0')).toBeInTheDocument()
    await expect.element(getByText('eth1')).toBeInTheDocument()
    await expect.element(getByText('Link Up')).toBeInTheDocument()
    await expect.element(getByText('Down')).toBeInTheDocument()
    await expect.element(getByText('当前管理连接')).toBeInTheDocument()
    await expect.element(getByText('默认', { exact: true })).toBeInTheDocument()
    await expect.element(getByText('192.168.1.100/24')).toBeInTheDocument()
  })

  it('triggers onEdit callback when configure button is clicked', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByRole } = await render(
      <InterfaceTable
        interfaces={mockInterfaces}
        isLoading={false}
        isError={false}
        error={null}
        onRefetch={vi.fn()}
        onEdit={onEdit}
        onPing={onPing}
      />
    )

    const editBtns = getByRole('button', { name: /配置/i })
    await userEvent.click(editBtns.first())
    expect(onEdit).toHaveBeenCalledWith(mockInterfaces[0])
  })

  it('triggers onPing callback when probe button is clicked', async () => {
    const onEdit = vi.fn()
    const onPing = vi.fn()

    const { getByRole } = await render(
      <InterfaceTable
        interfaces={mockInterfaces}
        isLoading={false}
        isError={false}
        error={null}
        onRefetch={vi.fn()}
        onEdit={onEdit}
        onPing={onPing}
      />
    )

    const pingBtns = getByRole('button', { name: /探测连通性/i })
    await userEvent.click(pingBtns.first())
    expect(onPing).toHaveBeenCalledWith('192.168.1.1')
  })

  it('renders empty message when no interfaces detected', async () => {
    const { getByText } = await render(
      <InterfaceTable
        interfaces={[]}
        isLoading={false}
        isError={false}
        error={null}
        onRefetch={vi.fn()}
        onEdit={vi.fn()}
        onPing={vi.fn()}
      />
    )

    await expect
      .element(getByText('未探测到任何物理网络接口'))
      .toBeInTheDocument()
  })
})
