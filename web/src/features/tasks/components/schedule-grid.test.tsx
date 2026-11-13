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

  it('toggles a whole day when clicking day label button in daily mode', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {},
    })
    const { getByText } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    await userEvent.click(getByText('按天明细'))
    await userEvent.click(getByText('周一'))

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.days?.monday).toEqual([{ start: '00:00', end: '24:00' }])
  })

  it('switches between grouped and daily mode seamlessly', async () => {
    const activeValue = JSON.stringify({
      enabled: true,
      days: {
        monday: [{ start: '08:00', end: '18:00' }],
      },
    })
    const { getByText } = await render(
      <ScheduleGrid value={activeValue} onChange={vi.fn()} />
    )

    await expect.element(getByText(/工作日 \(周一至周五\)/)).toBeInTheDocument()
    await userEvent.click(getByText('按天明细'))
    await expect.element(getByText('周一')).toBeInTheDocument()
    await expect.element(getByText('周日')).toBeInTheDocument()
  })

  it('removes an interval when clicking remove button on pill', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {
        monday: [
          { start: '08:00', end: '12:00' },
          { start: '14:00', end: '18:00' },
        ],
      },
    })
    const { getByTitle } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    // In grouped mode, workday reflects monday
    const removeBtns = getByTitle('删除时段')
    await userEvent.click(removeBtns.first())

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.days?.monday).toEqual([{ start: '14:00', end: '18:00' }])
  })

  it('adds an interval inline directly when clicking add button', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {},
    })
    const { getByRole } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    // Click inline add button directly without popover
    await userEvent.click(getByRole('button', { name: /添加.*时段/ }).first())

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.days?.monday).toEqual([{ start: '08:00', end: '18:00' }])
    expect(parsed.days?.friday).toEqual([{ start: '08:00', end: '18:00' }])
  })

  it('updates precise minute in time input and keeps end input focused on Tab', async () => {
    const handleChange = vi.fn()
    const activeValue = JSON.stringify({
      enabled: true,
      days: {
        monday: [{ start: '08:00', end: '18:00' }],
      },
    })
    const { getByPlaceholder } = await render(
      <ScheduleGrid value={activeValue} onChange={handleChange} />
    )

    const startInput = getByPlaceholder('08:00').first()
    await userEvent.clear(startInput)
    await userEvent.type(startInput, '08:45')
    await userEvent.tab()

    expect(handleChange).toHaveBeenCalled()
    const emitted = handleChange.mock.calls[0][0]
    const parsed = parseSchedule(emitted)
    expect(parsed.days?.monday).toEqual([{ start: '08:45', end: '18:00' }])
    // 回归：此前 key 用 `${r.start}-${r.end}` 导致改完 start 后整行卸载，end 输入框丢焦点。
    const endInput = getByPlaceholder('08:00').nth(1).element()
    expect(document.activeElement).toBe(endInput)
  })
})
