import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { AuditDateRangePicker } from './AuditDateRangePicker'

describe('AuditDateRangePicker', () => {
  it('ignores invalid route dates when opening the calendar', async () => {
    const { getByRole, getByText } = await render(
      <AuditDateRangePicker
        startTime='not-a-date'
        endTime='also-invalid'
        onRangeChange={vi.fn()}
      />
    )

    await getByRole('button').click()
    await expect.element(getByText('快捷范围')).toBeInTheDocument()
  })
})
