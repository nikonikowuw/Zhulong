import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { PersonsActionDialog } from './persons-action-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

vi.mock('../hooks/use-persons', () => ({
  useCreatePerson: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
  useUpdatePerson: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
}))

function TestComponent() {
  const { setOpen } = usePersonsContext()
  return (
    <div>
      <Button onClick={() => setOpen('create')}>Open Create Person</Button>
      <PersonsActionDialog />
    </div>
  )
}

function EditTestComponent({
  person,
}: {
  person: { id: string; name: string; personId?: string | null }
}) {
  const { setOpen, setCurrentRow } = usePersonsContext()
  return (
    <div>
      <Button
        onClick={() => {
          setCurrentRow({
            ...person,
            createdAt: '2026-10-14T00:00:00Z',
            updatedAt: '2026-10-14T00:00:00Z',
            faces: [],
          })
          setOpen('edit')
        }}
      >
        Open Edit Person
      </Button>
      <PersonsActionDialog />
    </div>
  )
}

describe('PersonsActionDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders create dialog with name and personId fields when opened', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Create Person/i })
    await openBtn.click()

    await expect
      .element(getByRole('heading', { name: /新增人员档案/i }))
      .toBeInTheDocument()

    await expect
      .element(getByPlaceholder(/请输入人员姓名/i))
      .toBeInTheDocument()

    await expect
      .element(getByRole('button', { name: /确定/i }))
      .toBeInTheDocument()
  })

  it('displays localized validation error when submitting empty name', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Create Person/i })
    await openBtn.click()

    const submitBtn = getByRole('button', { name: /确定/i })
    await submitBtn.click()

    await expect
      .element(getByText(/请输入人员姓名/i))
      .toBeInTheDocument()
  })

  it('enables personId input when editing a person without assigned personId', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <EditTestComponent
            person={{ id: 'p_unbound', name: '无工号员工', personId: null }}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Edit Person/i })
    await openBtn.click()

    await expect
      .element(getByRole('heading', { name: /编辑人员档案/i }))
      .toBeInTheDocument()

    const personIdInput = getByPlaceholder(/选填，如：EMP-1001/i)
    await expect.element(personIdInput).toBeInTheDocument()
    await expect.element(personIdInput).not.toBeDisabled()
  })

  it('disables personId input when editing a person with assigned personId', async () => {
    const queryClient = new QueryClient()

    const { getByRole, getByPlaceholder } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <EditTestComponent
            person={{ id: 'p_bound', name: '已有工号员工', personId: 'EMP-001' }}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const openBtn = getByRole('button', { name: /Open Edit Person/i })
    await openBtn.click()

    const personIdInput = getByPlaceholder(/选填，如：EMP-1001/i)
    await expect.element(personIdInput).toBeInTheDocument()
    await expect.element(personIdInput).toBeDisabled()
  })
})
