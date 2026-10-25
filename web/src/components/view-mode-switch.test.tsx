import { LayoutGrid, List } from 'lucide-react'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { ViewModeSwitch } from './view-mode-switch'

const OPTIONS = [
  { value: 'table', label: '表格视图', icon: List },
  { value: 'grid', label: '画廊视图', icon: LayoutGrid },
] as const

describe('ViewModeSwitch', () => {
  it('renders one toggle per option and marks the active one', async () => {
    const { getByRole } = await render(
      <ViewModeSwitch value='table' options={OPTIONS} onChange={vi.fn()} />
    )

    await expect
      .element(getByRole('button', { name: '表格视图' }))
      .toHaveAttribute('aria-pressed', 'true')
    await expect
      .element(getByRole('button', { name: '画廊视图' }))
      .toHaveAttribute('aria-pressed', 'false')
  })

  it('emits the clicked option value', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <ViewModeSwitch value='table' options={OPTIONS} onChange={onChange} />
    )

    await getByRole('button', { name: '画廊视图' }).click()

    expect(onChange).toHaveBeenCalledExactlyOnceWith('grid')
  })
})
