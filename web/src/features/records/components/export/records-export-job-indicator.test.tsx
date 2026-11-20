import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { useExportJobStore } from '../../lib/export-job-store'
import { RecordsExportJobIndicator } from './records-export-job-indicator'

const navigate = vi.fn()

const mocks = vi.hoisted(() => ({
  getLatestPendingTask: vi.fn(),
  clearTaskDraft: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => navigate }
})

vi.mock('../../lib/export-db', () => ({
  exportDb: {
    getLatestPendingTask: mocks.getLatestPendingTask,
    clearTaskDraft: mocks.clearTaskDraft,
  },
}))

function makeDraft(overrides: Record<string, unknown> = {}) {
  return {
    taskId: 'task-1',
    mode: 'batch',
    activeTab: 'recognitions',
    tabs: ['recognitions'],
    recordCount: 10,
    totalCount: 10,
    completedKeys: ['a', 'b', 'c'],
    options: {},
    status: 'in_progress',
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  }
}

describe('RecordsExportJobIndicator', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getLatestPendingTask.mockResolvedValue(null)
    mocks.clearTaskDraft.mockResolvedValue(undefined)
    useExportJobStore.setState({
      status: 'idle',
      progress: null,
      error: null,
      failedImages: 0,
      resumeRequested: false,
    })
  })

  it('tells the user how much is already saved instead of promising a background job', async () => {
    useExportJobStore.setState({
      status: 'running',
      progress: {
        phase: 'processing_images',
        current: 2,
        total: 4,
        percent: 50,
        saved: 2,
      },
    })

    const screen = await render(<RecordsExportJobIndicator />)

    await expect.element(screen.getByText('导出进行中')).toBeInTheDocument()
    await expect
      .element(screen.getByText('已保存 2 / 4 张，中断后可继续'))
      .toBeInTheDocument()
    await expect
      .element(screen.getByText('请保持此标签页打开，已完成的图片已经保存'))
      .toBeInTheDocument()
    // 运行中不展示旧草稿，避免两张卡抢版面
    expect(screen.container.textContent).not.toContain('上次导出未完成')
  })

  it('surfaces an unfinished export even when the user never reopens the dialog', async () => {
    mocks.getLatestPendingTask.mockResolvedValue(makeDraft())

    const screen = await render(<RecordsExportJobIndicator />)

    await expect.element(screen.getByText('上次导出未完成')).toBeInTheDocument()
    await expect
      .element(screen.getByText('已保存 3 / 10 张，继续只需补做剩余部分'))
      .toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '继续导出' }))

    expect(useExportJobStore.getState().resumeRequested).toBe(true)
    expect(navigate).toHaveBeenCalledTimes(1)
    expect(navigate.mock.calls[0][0]).toMatchObject({ to: '/records' })
  })

  it('clears the saved draft when the user discards it', async () => {
    mocks.getLatestPendingTask.mockResolvedValue(makeDraft())

    const screen = await render(<RecordsExportJobIndicator />)

    await expect.element(screen.getByText('上次导出未完成')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '丢弃' }))

    expect(mocks.clearTaskDraft).toHaveBeenCalledWith('task-1')
    await vi.waitFor(() => {
      expect(screen.container.textContent).not.toContain('上次导出未完成')
    })
  })

  it('ignores a draft that saved nothing yet', async () => {
    mocks.getLatestPendingTask.mockResolvedValue(
      makeDraft({ completedKeys: [] })
    )

    const screen = await render(<RecordsExportJobIndicator />)

    await vi.waitFor(() => {
      expect(mocks.getLatestPendingTask).toHaveBeenCalled()
    })
    expect(screen.container.textContent).not.toContain('上次导出未完成')
  })
})
