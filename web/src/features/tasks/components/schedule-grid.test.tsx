import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { parseSchedule } from '../data/schedule-config'
import { ScheduleGrid } from './schedule-grid'

describe('ScheduleGrid', () => {
  it('renders default always-active state with switch turned off', async () => {
    const { getByText, getByRole } = await render(
      <ScheduleGrid value='{}' onChange={vi.fn()} />
    )
    await expect.element(getByText('分时布防计划')).toBeInTheDocument()
    await expect.element(getByText('24×7 全天运行')).toBeInTheDocument()
    expect(getByRole('switch').element().getAttribute('aria-checked')).toBe(
      'false'
    )
  })

  it('toggles switch to enable schedule and emits initial grid', async () => {
    const handleChange = vi.fn()
    const { getByRole } = await render(
      <ScheduleGrid value='{}' onChange={handleChange} />
    )

    await userEvent.click(getByRole('switch'))

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    expect(emitted).not.toBe('{}')
    const parsed = parseSchedule(emitted)
    expect(parsed.enabled).toBe(true)
  })

  it('applies workday preset and generates correct intervals', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {},
    })
    const { getByText } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    await userEvent.click(getByText('工作日 (08:00-18:00)'))

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.enabled).toBe(true)
    expect(parsed.days?.monday).toEqual([{ start: '08:00', end: '18:00' }])
    expect(parsed.days?.friday).toEqual([{ start: '08:00', end: '18:00' }])
    expect(parsed.days?.saturday).toBeUndefined()
  })

  it('applies clear preset', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: { monday: [{ start: '08:00', end: '18:00' }] },
    })
    const { getByText } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    await userEvent.click(getByText('清空'))

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.enabled).toBe(true)
    expect(parsed.days?.monday).toBeUndefined()
  })

  it('toggles a whole day when clicking day label button', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {},
    })
    const { getByText } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    await userEvent.click(getByText('周一'))

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.days?.monday).toEqual([{ start: '00:00', end: '24:00' }])
  })
})
