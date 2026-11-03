import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { exportDb } from '../../lib/export-db'
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

  it('exposes accessible names for the export switches', async () => {
    const screen = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={vi.fn()}
        activeTab='recognitions'
        mode='batch'
      />
    )

    await expect
      .element(screen.getByRole('switch', { name: '结构化数据报表 (CSV)' }))
      .toBeChecked()
    await expect
      .element(screen.getByRole('switch', { name: '抓拍图片证据包 (ZIP)' }))
      .not.toBeChecked()
  })

  it('toggles the CSV switch from its label text', async () => {
    const screen = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={vi.fn()}
        activeTab='recognitions'
        mode='batch'
      />
    )

    await userEvent.click(screen.getByText('结构化数据报表 (CSV)'))
    await expect
      .element(screen.getByRole('switch', { name: '结构化数据报表 (CSV)' }))
      .not.toBeChecked()
  })

  it('detects pending draft and allows discarding it', async () => {
    await exportDb.saveTaskDraft({
      taskId: 'draft-detect-01',
      mode: 'selection',
      activeTab: 'recognitions',
      tabs: ['recognitions'],
      recordCount: 100,
      totalCount: 100,
      completedKeys: ['k1', 'k2', 'k3'],
      options: {
        exportCsv: true,
        exportImages: true,
        burnOverlay: true,
        imageKind: 'scene',
      },
      status: 'paused',
      createdAt: Date.now(),
      updatedAt: Date.now(),
    })

    const { getByText } = await render(
      <RecordsExportDialog
        open={true}
        onOpenChange={vi.fn()}
        activeTab='recognitions'
        mode='selection'
      />
    )

    await expect
      .element(getByText('发现上次未完成的导出任务'))
      .toBeInTheDocument()
    await expect.element(getByText(/3 \/ 100/)).toBeInTheDocument()

    const discardBtn = getByText('放弃')
    await userEvent.click(discardBtn)

    const draftAfter = await exportDb.getTaskDraft('draft-detect-01')
    expect(draftAfter).toBeNull()
  })
})
