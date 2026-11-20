import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { recordsApi } from '../api/records-api'
import { RecordsToolbar } from './records-toolbar'

vi.mock('@/features/cameras/contract', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/cameras/contract')>()
  return {
    ...actual,
    useAllCameras: () => ({
      data: { items: [], total: 0, page: 1, pageSize: 100 },
    }),
  }
})

function renderWithQuery(ui: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
  )
}

describe('RecordsToolbar', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('fetches alarmTypes / targetTypes only on their corresponding tabs and debounces the person keyword input', async () => {
    const alarmTypesSpy = vi
      .spyOn(recordsApi, 'getAlarmTypes')
      .mockResolvedValue(['tripwire'])
    const targetTypesSpy = vi
      .spyOn(recordsApi, 'getCaptureTargetTypes')
      .mockResolvedValue(['person'])

    const onSearchChange = vi.fn()

    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='recognitions'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onSearchChange={onSearchChange}
        onResetFilters={vi.fn()}
      />
    )

    expect(alarmTypesSpy).not.toHaveBeenCalled()
    expect(targetTypesSpy).not.toHaveBeenCalled()

    const input = screen.getByPlaceholder('按姓名或工号搜索')
    await userEvent.type(input, '张三')

    // 防抖窗口期内不应立刻触发逐字符回调
    expect(onSearchChange).not.toHaveBeenCalled()

    await vi.waitFor(
      () => {
        expect(onSearchChange).toHaveBeenCalledExactlyOnceWith('张三')
      },
      { timeout: 1000 }
    )
  })

  it('shows a removable chip for a deep-linked person id instead of a raw id input', async () => {
    const onPersonIdChange = vi.fn()
    const onSearchChange = vi.fn()

    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='recognitions'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        personId='person_alice'
        personName='Alice'
        search='EMP-0'
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onPersonIdChange={onPersonIdChange}
        onSearchChange={onSearchChange}
        onResetFilters={vi.fn()}
      />
    )

    // 关键字框只承载人名/工号，深链的人员 ID 不能显示成可编辑文本
    const input = screen.getByPlaceholder('按姓名或工号搜索')
    await expect.element(input).toHaveValue('EMP-0')
    await expect.element(screen.getByText('人员：Alice')).toBeVisible()

    await userEvent.click(screen.getByRole('button', { name: '清除人员筛选' }))
    expect(onPersonIdChange).toHaveBeenCalledExactlyOnceWith(undefined)
    expect(onSearchChange).not.toHaveBeenCalled()
  })
})

describe('RecordsToolbar dedup toggle', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('toggles display dedup on the recognitions tab', async () => {
    const onDedupChange = vi.fn()

    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='recognitions'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        dedup={false}
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onDedupChange={onDedupChange}
        onResetFilters={vi.fn()}
      />
    )

    const button = screen.getByRole('button', { name: '去重' })
    await expect.element(button).toHaveAttribute('aria-pressed', 'false')

    await userEvent.click(button)
    expect(onDedupChange).toHaveBeenCalledExactlyOnceWith(true)
  })

  it('marks the toggle as pressed when dedup is active', async () => {
    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='recognitions'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        dedup
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onResetFilters={vi.fn()}
      />
    )

    await expect
      .element(screen.getByRole('button', { name: '去重' }))
      .toHaveAttribute('aria-pressed', 'true')
  })

  it('hides the toggle on other tabs', async () => {
    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='alarms'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onResetFilters={vi.fn()}
      />
    )

    expect(screen.getByRole('button', { name: '去重' }).query()).toBeNull()
  })
})
