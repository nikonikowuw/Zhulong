import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { RecordsExportDialog } from './records-export-dialog'

describe('RecordsExportDialog', () => {
  it('renders selection mode correctly', async () => {
    const handleOpenChange = vi.fn()
    const { getByText } = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={handleOpenChange}
        activeTab='recognitions'
        mode='selection'
        selectedItems={[
          {
            id: 'r-1',
            cameraId: 'c-1',
            algorithmId: 'algo-1',
            pts: 0,
            trackId: 1,
            personId: null,
            personExternalId: '',
            personName: 'Test',
            matchedFaceId: '',
            matched: false,
            similarity: 0,
            candidates: [],
            faceImagePath: '',
            galleryImagePath: '',
            imagePath: '',
            box: [0, 0, 0, 0],
            bodyBox: [0, 0, 0, 0],
            attributes: {},
            capturedAt: '2026-11-03T10:00:00Z',
            createdAt: '',
          },
        ]}
      />
    )

    await expect.element(getByText('导出选中感知记录')).toBeInTheDocument()
    await expect.element(getByText(/已锁定勾选的 1 条记录/)).toBeInTheDocument()
  })

  it('renders batch mode with tabs selection', async () => {
    const { getByText } = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={vi.fn()}
        activeTab='alarms'
        mode='batch'
        estimatedTotal={120}
      />
    )

    await expect.element(getByText('批量导出感知记录')).toBeInTheDocument()
    await expect
      .element(getByText(/当前 Tab 预估匹配 120 条/))
      .toBeInTheDocument()
    await expect.element(getByText('告警记录')).toBeInTheDocument()
  })

  it('handles cancel button click', async () => {
    const handleOpenChange = vi.fn()
    const { getByRole } = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={handleOpenChange}
        activeTab='recognitions'
        mode='batch'
      />
    )

    const cancelBtn = getByRole('button', { name: '取消' })
    await userEvent.click(cancelBtn)
    expect(handleOpenChange).toHaveBeenCalledWith(false)
  })
})
