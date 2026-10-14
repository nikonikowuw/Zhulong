import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Person } from '../data/schema'
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
    await expect.element(getByText('Alice Cooper')).toBeInTheDocument()
    await expect.element(getByText('EMP-001')).toBeInTheDocument()
    await expect.element(getByText('p_test_01')).toBeInTheDocument()
  })
})
