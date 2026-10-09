import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { WatchdogModal, type WatchdogTransactionData } from './watchdog-modal'

const confirmMutateMock = vi.fn()
const rollbackMutateMock = vi.fn()

vi.mock('../hooks/use-network', () => ({
  useConfirmMutation: () => ({
    mutateAsync: confirmMutateMock,
    isPending: false,
  }),
  useRollbackMutation: () => ({
    mutateAsync: rollbackMutateMock,
    isPending: false,
  }),
}))

const mockTx: WatchdogTransactionData = {
  transactionId: 'tx-123',
  timeoutSec: 60,
  targetUrl: 'http://192.168.1.120:8080/settings/network',
  confirmToken: 'tok-safe',
}

describe('WatchdogModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders countdown and warning info', async () => {
    const { getByRole, getByText } = await render(
      <WatchdogModal
        open
        onOpenChange={vi.fn()}
        transaction={mockTx}
        onConfirmed={vi.fn()}
        onRolledBack={vi.fn()}
      />
    )

    await expect
      .element(getByRole('heading', { name: /网络配置/i }))
      .toBeInTheDocument()
    await expect.element(getByText('60 秒')).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /确认/i }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /放弃/i }))
      .toBeInTheDocument()
  })

  it('triggers confirm mutation on confirm button click', async () => {
    confirmMutateMock.mockResolvedValueOnce({ status: 'confirmed' })
    const onConfirmed = vi.fn()

    const { getByRole } = await render(
      <WatchdogModal
        open
        onOpenChange={vi.fn()}
        transaction={mockTx}
        onConfirmed={onConfirmed}
        onRolledBack={vi.fn()}
      />
    )

    await userEvent.click(getByRole('button', { name: /确认/i }))
    await vi.waitFor(() => {
      expect(confirmMutateMock).toHaveBeenCalledWith('tok-safe')
      expect(onConfirmed).toHaveBeenCalledOnce()
    })
  })

  it('triggers rollback mutation on rollback button click', async () => {
    rollbackMutateMock.mockResolvedValueOnce({ status: 'rolled_back' })
    const onRolledBack = vi.fn()

    const { getByRole } = await render(
      <WatchdogModal
        open
        onOpenChange={vi.fn()}
        transaction={mockTx}
        onConfirmed={vi.fn()}
        onRolledBack={onRolledBack}
      />
    )

    await userEvent.click(getByRole('button', { name: /放弃/i }))
    await vi.waitFor(() => {
      expect(rollbackMutateMock).toHaveBeenCalledWith('tok-safe')
      expect(onRolledBack).toHaveBeenCalledOnce()
    })
  })
})
