import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type AuditLog } from '../data/schema'
import { StatusBadge } from './AuditColumnsCells'
import { AuditProvider } from './AuditProvider'
import { AuditTable } from './AuditTable'

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => ({
    pagination: { pageIndex: 0, pageSize: 20 },
    onPaginationChange: vi.fn(),
    ensurePageInRange: vi.fn(),
    globalFilter: '',
    onGlobalFilterChange: vi.fn(),
    columnFilters: [],
    onColumnFiltersChange: vi.fn(),
  }),
}))

const mockLogs: AuditLog[] = [
  {
    id: 101,
    createdAt: '2026-10-10T10:00:00Z',
    ip: '192.168.1.55',
    username: 'admin',
    action: 'camera.create',
    target: 'camera:1',
    detail: '{"name":"Gate Camera"}',
    status: 'success',
    errorMsg: '',
  },
  {
    id: 102,
    createdAt: '2026-10-10T10:05:00Z',
    ip: '192.168.1.66',
    username: 'admin',
    action: 'auth.login',
    target: 'system',
    detail: '',
    status: 'failed',
    errorMsg: 'Invalid credentials',
  },
]

describe('AuditTable', () => {
  it('uses the neutral fallback for an unknown audit status', async () => {
    const { getByText } = await render(<StatusBadge status='unknown' />)

    await expect.element(getByText('unknown')).toHaveClass(/border-neutral-300/)
  })

  it('renders table headers and audit rows properly', async () => {
    const queryClient = new QueryClient()

    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <AuditProvider>
          <AuditTable
            data={mockLogs}
            total={2}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
            onDateRangeChange={vi.fn()}
          />
        </AuditProvider>
      </QueryClientProvider>
    )

    await expect.element(getByRole('table')).toBeInTheDocument()
    await expect.element(getByText('动作类型')).toBeInTheDocument()
    await expect.element(getByText('状态筛选')).toBeInTheDocument()
    await expect.element(getByText('时间范围')).toBeInTheDocument()
    await expect.element(getByText('#101')).toBeInTheDocument()
    await expect.element(getByText('添加摄像机')).toBeInTheDocument()
    await expect.element(getByText('#102')).toBeInTheDocument()
    await expect.element(getByText('管理员登录')).toBeInTheDocument()
  })

  it('renders empty state when there are no logs', async () => {
    const queryClient = new QueryClient()

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AuditProvider>
          <AuditTable
            data={[]}
            total={0}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
            onDateRangeChange={vi.fn()}
          />
        </AuditProvider>
      </QueryClientProvider>
    )

    await expect.element(getByText('暂无审计日志记录')).toBeInTheDocument()
  })

  it('renders time preset badge properly', async () => {
    const queryClient = new QueryClient()

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <AuditProvider>
          <AuditTable
            data={mockLogs}
            total={2}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{ preset: '1h' }}
            navigate={vi.fn()}
            preset='1h'
            onDateRangeChange={vi.fn()}
          />
        </AuditProvider>
      </QueryClientProvider>
    )

    await expect.element(getByText('近 1 小时')).toBeInTheDocument()
  })
})
