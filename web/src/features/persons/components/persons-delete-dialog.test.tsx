import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { personsApi } from '../api/persons-api'
import { type Person } from '../data/schema'
import { PersonsDeleteDialog } from './persons-delete-dialog'
import { PersonsMultiDeleteDialog } from './persons-multi-delete-dialog'
import { PersonsProvider, usePersonsActions } from './persons-provider'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    deletePerson: vi.fn(),
    getLatestFaceReextractJob: vi.fn(),
  },
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
    warning: vi.fn(),
    info: vi.fn(),
  },
}))

const samplePersons: Person[] = [
  {
    id: 'p-1',
    personId: 'EMP001',
    name: 'Alice',
    source: 'LOCAL',
    sourceName: '',
    faces: [],
    createdAt: '2026-10-14T00:00:00Z',
    updatedAt: '2026-10-14T00:00:00Z',
  },
  {
    id: 'p-2',
    personId: 'EMP002',
    name: 'Bob',
    source: 'LOCAL',
    sourceName: '',
    faces: [],
    createdAt: '2026-10-14T00:00:00Z',
    updatedAt: '2026-10-14T00:00:00Z',
  },
]

function SingleDeleteHarness() {
  const { setCurrentRow, setOpen } = usePersonsActions()
  return (
    <>
      <Button
        onClick={() => {
          setCurrentRow(samplePersons[0])
          setOpen('delete')
        }}
      >
        Open Delete
      </Button>
      <PersonsDeleteDialog />
    </>
  )
}

const columns: ColumnDef<Person>[] = [{ accessorKey: 'name' }]

function MultiDeleteHarness() {
  const [open, setOpen] = useState(false)
  const [rowSelection, setRowSelection] = useState<Record<string, boolean>>({
    'p-1': true,
    'p-2': true,
  })
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: samplePersons,
    columns,
    state: { rowSelection },
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    getRowId: (row) => row.id,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <>
      <Button onClick={() => setOpen(true)}>Open Multi Delete</Button>
      <PersonsMultiDeleteDialog
        open={open}
        onOpenChange={setOpen}
        table={table}
      />
    </>
  )
}

describe('PersonsDeleteDialog & PersonsMultiDeleteDialog', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(personsApi.getLatestFaceReextractJob).mockResolvedValue(null)
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
  })

  it('deletes a single person upon confirmation', async () => {
    vi.mocked(personsApi.deletePerson).mockResolvedValueOnce(undefined)

    const { getByRole } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <SingleDeleteHarness />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Delete' }).click()
    await getByRole('button', { name: '删除人员' }).click()

    await expect
      .poll(() => vi.mocked(personsApi.deletePerson).mock.calls.length)
      .toBe(1)
    expect(personsApi.deletePerson).toHaveBeenCalledWith('p-1')
    expect(toast.success).toHaveBeenCalledWith('人员档案已删除')
  })

  it('requires typing the confirm word before batch delete and reports partial failures', async () => {
    vi.mocked(personsApi.deletePerson)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('DB locked'))

    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <MultiDeleteHarness />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Multi Delete' }).click()
    const confirmBtn = getByRole('button', { name: '批量删除' })
    await expect.element(confirmBtn).toBeDisabled()

    await getByPlaceholder('删除').fill('删除')
    await expect.element(confirmBtn).toBeEnabled()
    await confirmBtn.click()

    await expect
      .poll(() => vi.mocked(personsApi.deletePerson).mock.calls.length)
      .toBe(2)
    expect(toast.warning).toHaveBeenCalledWith('成功删除 1 人，1 人失败')
  })
})
