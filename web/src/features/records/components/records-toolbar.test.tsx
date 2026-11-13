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

  it('fetches alarmTypes / targetTypes only on their corresponding tabs and debounces personId input', async () => {
    const alarmTypesSpy = vi
      .spyOn(recordsApi, 'getAlarmTypes')
      .mockResolvedValue(['tripwire'])
    const targetTypesSpy = vi
      .spyOn(recordsApi, 'getCaptureTargetTypes')
      .mockResolvedValue(['person'])

    const onPersonIdChange = vi.fn()

    const screen = await renderWithQuery(
      <RecordsToolbar
        tab='recognitions'
        view='table'
        dateRange={{ isDefaultWindow: true, preset: 'today' }}
        onViewChange={vi.fn()}
        onCameraChange={vi.fn()}
        onDateRangeChange={vi.fn()}
        onPersonIdChange={onPersonIdChange}
        onResetFilters={vi.fn()}
      />
    )

    expect(alarmTypesSpy).not.toHaveBeenCalled()
    expect(targetTypesSpy).not.toHaveBeenCalled()

    const input = screen.getByPlaceholder('输入人员 ID 搜索')
    await userEvent.type(input, 'p-123')

    // 防抖窗口期内不应立刻触发逐字符回调
    expect(onPersonIdChange).not.toHaveBeenCalled()

    await vi.waitFor(
      () => {
        expect(onPersonIdChange).toHaveBeenCalledExactlyOnceWith('p-123')
      },
      { timeout: 1000 }
    )
  })
})
