import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { auditApi } from '../api/audit-api'
import { type AuditLog, type GetAuditLogsParams } from '../data/schema'
import { AuditPrimaryButtons } from './AuditPrimaryButtons'

const { triggerDownload } = vi.hoisted(() => ({ triggerDownload: vi.fn() }))
const { toastError, toastInfo, toastSuccess, toastWarning } = vi.hoisted(
  () => ({
    toastError: vi.fn(),
    toastInfo: vi.fn(),
    toastSuccess: vi.fn(),
    toastWarning: vi.fn(),
  })
)

// 只替换真正触发下载的副作用，CSV 组装仍走真实实现
vi.mock('@/lib/csv', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/csv')>()
  return { ...actual, triggerDownload }
})

vi.mock('sonner', () => ({
  toast: {
    error: toastError,
    info: toastInfo,
    success: toastSuccess,
    warning: toastWarning,
  },
}))

vi.mock('../api/audit-api', () => ({
  auditApi: { getAuditLogs: vi.fn() },
}))

const FILTER = {
  actions: ['auth.login'],
  status: 'success',
  startTime: '2026-11-01T00:00:00Z',
}

function makeLog(overrides: Partial<AuditLog> = {}): AuditLog {
  return {
    id: 1,
    createdAt: '2026-11-06T10:00:00Z',
    ip: '192.168.1.100',
    username: 'admin',
    action: 'auth.login',
    target: 'system',
    detail: '',
    status: 'success',
    errorMsg: '',
    ...overrides,
  }
}

/** 独立实现的本地时间格式化：不与被测实现共用同一段代码 */
function localStamp(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

async function renderButtons(props: {
  onRefresh?: () => void
  isRefreshing?: boolean
  filter?: GetAuditLogsParams
}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <AuditPrimaryButtons {...props} />
    </QueryClientProvider>
  )
}

async function lastCsv(): Promise<{
  text: string
  bytes: Uint8Array
  filename: string
}> {
  await vi.waitFor(() => expect(triggerDownload).toHaveBeenCalled())
  const calls = triggerDownload.mock.calls
  const [blob, filename] = calls[calls.length - 1] as [Blob, string]
  const bytes = new Uint8Array(await blob.arrayBuffer())
  // Blob.text() 会在 UTF-8 解码时吞掉 BOM，所以字节单独拿出来验
  return { text: await blob.text(), bytes, filename }
}

describe('AuditPrimaryButtons', () => {
  beforeEach(() => {
    vi.mocked(auditApi.getAuditLogs).mockReset()
    triggerDownload.mockReset()
    toastError.mockReset()
    toastInfo.mockReset()
    toastSuccess.mockReset()
    toastWarning.mockReset()
  })

  it('triggers onRefresh when clicking refresh button', async () => {
    const onRefresh = vi.fn()
    const { getByRole } = await renderButtons({
      onRefresh,
      isRefreshing: false,
    })

    await userEvent.click(getByRole('button', { name: '刷新' }))
    expect(onRefresh).toHaveBeenCalledOnce()
  })

  it('exports the current filter as CSV instead of the first page of everything', async () => {
    vi.mocked(auditApi.getAuditLogs).mockResolvedValue({
      items: [
        makeLog({ id: 7, detail: '{"name":"Gate, East"}' }),
        makeLog({
          id: 8,
          action: 'system.unregistered_action',
          status: 'failed',
        }),
      ],
      total: 2,
      page: 1,
      pageSize: 100,
    })

    const { getByRole } = await renderButtons({ filter: FILTER })
    await userEvent.click(getByRole('button', { name: '导出日志' }))

    // 请求必须带上归一化后的筛选条件，并落到服务端允许的最大页长
    expect(auditApi.getAuditLogs).toHaveBeenCalledExactlyOnceWith({
      ...FILTER,
      page: 1,
      pageSize: 100,
    })

    const { text, bytes, filename } = await lastCsv()
    expect(filename).toMatch(/^zhulong_audit_logs_\d{8}_\d{6}\.csv$/)
    // Excel 打开中文 CSV 依赖 UTF-8 BOM
    expect(Array.from(bytes.slice(0, 3))).toEqual([0xef, 0xbb, 0xbf])

    const lines = text.split('\r\n')
    expect(lines[0]).toBe(
      '日志 ID,记录时间,操作人,操作动作,操作目标,客户端 IP,执行状态,详情'
    )
    expect(lines).toHaveLength(3)
    expect(lines[1]).toBe(
      `7,${localStamp('2026-11-06T10:00:00Z')},admin,管理员登录,system,192.168.1.100,成功,"{""name"":""Gate, East""}"`
    )
    // 未登记的动作/状态回退成原始 key，而不是本地化串被清空
    expect(lines[2]).toContain('system.unregistered_action')
    expect(lines[2]).toContain('失败')
    expect(toastSuccess).toHaveBeenCalledWith('已成功导出 2 条审计记录')
  })

  it('pages through the whole result set instead of stopping at the server page cap', async () => {
    const firstPage = Array.from({ length: 100 }, (_, i) =>
      makeLog({ id: i + 1 })
    )
    const secondPage = Array.from({ length: 50 }, (_, i) =>
      makeLog({ id: i + 101 })
    )

    vi.mocked(auditApi.getAuditLogs)
      .mockResolvedValueOnce({
        items: firstPage,
        total: 150,
        page: 1,
        pageSize: 100,
      })
      .mockResolvedValueOnce({
        items: secondPage,
        total: 150,
        page: 2,
        pageSize: 100,
      })

    const { getByRole } = await renderButtons({ filter: FILTER })
    await userEvent.click(getByRole('button', { name: '导出日志' }))

    const { text } = await lastCsv()
    expect(auditApi.getAuditLogs).toHaveBeenNthCalledWith(1, {
      ...FILTER,
      page: 1,
      pageSize: 100,
    })
    expect(auditApi.getAuditLogs).toHaveBeenNthCalledWith(2, {
      ...FILTER,
      page: 2,
      pageSize: 100,
    })
    expect(text.split('\r\n')).toHaveLength(151)
    expect(toastSuccess).toHaveBeenCalledWith('已成功导出 150 条审计记录')
  })

  it('stops at the hard row cap and says the export is partial', async () => {
    vi.mocked(auditApi.getAuditLogs).mockResolvedValueOnce({
      items: Array.from({ length: 5000 }, (_, i) => makeLog({ id: i + 1 })),
      total: 6000,
      page: 1,
      pageSize: 100,
    })

    const { getByRole } = await renderButtons({ filter: FILTER })
    await userEvent.click(getByRole('button', { name: '导出日志' }))

    const { text } = await lastCsv()
    expect(text.split('\r\n')).toHaveLength(5001)
    expect(auditApi.getAuditLogs).toHaveBeenCalledOnce()
    expect(toastWarning).toHaveBeenCalledWith(
      '记录过多，仅导出最新 5000 / 6000 条'
    )
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('reports the empty state without producing a file', async () => {
    vi.mocked(auditApi.getAuditLogs).mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      pageSize: 100,
    })

    const { getByRole } = await renderButtons({ filter: FILTER })
    await userEvent.click(getByRole('button', { name: '导出日志' }))

    await vi.waitFor(() =>
      expect(toastInfo).toHaveBeenCalledWith('当前筛选条件下无审计记录可导出')
    )
    expect(triggerDownload).not.toHaveBeenCalled()
  })

  it('surfaces the server message when the export request fails', async () => {
    vi.mocked(auditApi.getAuditLogs).mockRejectedValue(
      new Error('页面大小超出限制')
    )

    const { getByRole } = await renderButtons({ filter: FILTER })
    await userEvent.click(getByRole('button', { name: '导出日志' }))

    await vi.waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        '导出审计日志失败: 页面大小超出限制'
      )
    )
    expect(triggerDownload).not.toHaveBeenCalled()
  })
})
