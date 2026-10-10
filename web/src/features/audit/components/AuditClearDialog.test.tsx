import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { AuditClearDialog } from './AuditClearDialog'
import { AuditProvider, useAuditContext } from './AuditProvider'

const { mutateAsync } = vi.hoisted(() => ({ mutateAsync: vi.fn() }))

vi.mock('../hooks/use-audit-logs', () => ({
  useClearAuditLogs: () => ({ mutateAsync, isPending: false }),
}))

function ClearDialogTrigger() {
  const { openClear } = useAuditContext()
  return <button onClick={openClear}>Open clear dialog</button>
}

describe('AuditClearDialog', () => {
  beforeEach(() => {
    mutateAsync.mockReset()
    mutateAsync.mockResolvedValue({ cleared: 2 })
  })

  it('calls the clear mutation only after the explicit confirmation', async () => {
    const { getByRole } = await render(
      <AuditProvider>
        <ClearDialogTrigger />
        <AuditClearDialog />
      </AuditProvider>
    )

    await userEvent.click(getByRole('button', { name: 'Open clear dialog' }))
    await expect.element(getByRole('dialog')).toBeInTheDocument()
    expect(mutateAsync).not.toHaveBeenCalled()

    await userEvent.click(getByRole('radio', { name: '清空全部日志' }))
    expect(mutateAsync).not.toHaveBeenCalled()

    await userEvent.click(getByRole('button', { name: '确认永久删除' }))
    expect(mutateAsync).toHaveBeenCalledOnce()
    expect(mutateAsync).toHaveBeenCalledWith({ before: undefined })
  })
})
