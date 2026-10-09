import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { PingDialog } from './ping-dialog'

const mutateMock = vi.fn()

vi.mock('../hooks/use-network', () => ({
  usePingMutation: () => ({
    mutate: mutateMock,
    reset: vi.fn(),
    isPending: false,
    data: null,
    isError: false,
    error: null,
  }),
}))

describe('PingDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders target input and probe button', async () => {
    const { getByRole, getByPlaceholder } = await render(
      <PingDialog open onOpenChange={vi.fn()} defaultTarget='192.168.1.1' />
    )

    await expect
      .element(getByRole('heading', { name: /网络连通性/i }))
      .toBeInTheDocument()
    await expect
      .element(getByPlaceholder(/例如: 192.168.1.1/i))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /开始探测/i }))
      .toBeInTheDocument()
  })

  it('submits ping probe target when button is clicked', async () => {
    const { getByRole, getByPlaceholder } = await render(
      <PingDialog open onOpenChange={vi.fn()} defaultTarget='192.168.1.1' />
    )

    await userEvent.clear(getByPlaceholder(/例如: 192.168.1.1/i))
    await userEvent.fill(getByPlaceholder(/例如: 192.168.1.1/i), '10.0.0.1')
    await userEvent.click(getByRole('button', { name: /开始探测/i }))

    await vi.waitFor(() => {
      expect(mutateMock).toHaveBeenCalledWith('10.0.0.1')
    })
  })
})
