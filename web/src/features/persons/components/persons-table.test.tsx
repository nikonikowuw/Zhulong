import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Person } from '../data/schema'
import { PersonsDialogs } from './persons-dialogs'
import { PersonsProvider } from './persons-provider'
import { PersonsTable } from './persons-table'

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => ({
    pagination: { pageIndex: 0, pageSize: 10 },
    onPaginationChange: vi.fn(),
    ensurePageInRange: vi.fn(),
    globalFilter: '',
    onGlobalFilterChange: vi.fn(),
    columnFilters: [],
    onColumnFiltersChange: vi.fn(),
  }),
}))

const mockPersons: Person[] = [
  {
    id: 'p_test_01',
    personId: 'EMP-001',
    name: 'Alice Cooper',
    primaryFaceId: 'f_test_01',
    faces: [
      {
        id: 'f_test_01',
        ownerRowId: 'p_test_01',
        imagePath: '/data/faces/alice.jpg',
        createdAt: '2026-10-14T08:00:00Z',
      },
    ],
    createdAt: '2026-10-14T08:00:00Z',
    updatedAt: '2026-10-14T08:00:00Z',
  },
]

describe('PersonsTable', () => {
  it('renders table headers and person row data', async () => {
    const queryClient = new QueryClient()

    const { getByText, getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <PersonsTable
            data={mockPersons}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await expect.element(getByRole('table')).toBeInTheDocument()
    await expect
      .element(getByRole('checkbox', { name: /Select all/i }))
      .toBeInTheDocument()
    await expect.element(getByText(/人脸照片/i)).toBeInTheDocument()
    await expect.element(getByText('Alice Cooper')).toBeInTheDocument()
    await expect.element(getByText('EMP-001')).toBeInTheDocument()
  })

  it('clicking face avatar triggers full-size image preview dialog', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByTitle } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <PersonsTable
            data={mockPersons}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
          <PersonsDialogs />
        </PersonsProvider>
      </QueryClientProvider>
    )

    // 点击头像按钮
    const avatarBtn = getByTitle('Alice Cooper')
    await avatarBtn.click()

    // 弹出大图预览弹窗，展示大图标题
    await expect
      .element(getByRole('heading', { name: 'Alice Cooper (f_test_01)' }))
      .toBeInTheDocument()

    // 此时人脸底库管理弹窗不应该被打开
    expect(document.body.textContent).not.toContain('人脸底库管理')
  })

  it('selecting rows shows bulk actions toolbar with batch delete button', async () => {
    const queryClient = new QueryClient()

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <PersonsTable
            data={mockPersons}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const selectAllCheckbox = getByRole('checkbox', { name: /Select all/i })
    await selectAllCheckbox.click()

    // 批量操作栏出现，带有批量删除按钮
    const batchDeleteBtn = getByRole('button', { name: /批量删除/i })
    await expect.element(batchDeleteBtn).toBeInTheDocument()
  })

  it('renders "-" when personId is null or empty', async () => {
    const queryClient = new QueryClient()
    const unboundPerson: Person[] = [
      {
        id: 'p_unbound',
        personId: null,
        name: 'Bob Unbound',
        createdAt: '2026-10-14T08:00:00Z',
        updatedAt: '2026-10-14T08:00:00Z',
        faces: [],
      },
    ]

    const { getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <PersonsTable
            data={unboundPerson}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await expect.element(getByText('Bob Unbound')).toBeInTheDocument()
    await expect.element(getByText('-')).toBeInTheDocument()
  })

  it('switches between table and grid views when clicking view toggle', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByTestId } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <PersonsTable
            data={mockPersons}
            total={1}
            isLoading={false}
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    // 默认是表格模式
    await expect.element(getByRole('table')).toBeInTheDocument()

    // 切换到画廊视图
    const gridBtn = getByRole('button', { name: /画廊视图/i })
    await gridBtn.click()

    // 此时出现卡片视图
    await expect.element(getByTestId('person-card')).toBeInTheDocument()
  })
})
