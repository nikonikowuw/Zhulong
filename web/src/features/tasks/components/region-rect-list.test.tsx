import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type RegionDraft } from '../data/region-config'
import { RegionRectList } from './region-rect-list'

const draft: RegionDraft = {
  roi: [{ id: 'roi-1', rect: [0.1, 0.1, 0.5, 0.5] }],
  mask: [{ id: 'mask-1', rect: [0.6, 0.6, 0.8, 0.8] }],
}

describe('RegionRectList', () => {
  it('renders ROI and MASK items with calculated bottom-center points', async () => {
    const handleSelect = vi.fn()
    const handleDelete = vi.fn()

    const { getByText } = await render(
      <RegionRectList
        draft={draft}
        selected={null}
        onSelect={handleSelect}
        onDelete={handleDelete}
      />
    )

    await expect.element(getByText('ROI #1')).toBeInTheDocument()
    await expect.element(getByText('MASK #1')).toBeInTheDocument()
    // Bottom center for ROI: x=(0.1+0.5)/2=0.300, y=0.500
    await expect.element(getByText(/0.300, 0.500/)).toBeInTheDocument()
  })

  it('handles item selection and deletion', async () => {
    const handleSelect = vi.fn()
    const handleDelete = vi.fn()

    const { getByTitle, getByText } = await render(
      <RegionRectList
        draft={{ ...draft, mask: [] }}
        selected={{ category: 'roi', index: 0 }}
        onSelect={handleSelect}
        onDelete={handleDelete}
      />
    )

    const deleteBtn = getByTitle('删除区域')
    await deleteBtn.click()
    expect(handleDelete).toHaveBeenCalledWith('roi', 0)

    await getByText('ROI #1').click()
    expect(handleSelect).toHaveBeenCalledWith({ category: 'roi', index: 0 })
  })
})
