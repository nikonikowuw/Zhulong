import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { PersonsImportDialog } from './persons-import-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

const previewMutation = {
  mutateAsync: vi.fn(),
  isPending: false,
  isError: false,
  reset: vi.fn(),
}
const commitMutation = {
  mutateAsync: vi.fn(),
  isPending: false,
  isError: false,
  reset: vi.fn(),
}

vi.mock('../hooks/use-persons', () => ({
  usePreviewPersonImport: () => previewMutation,
  useCommitPersonImport: () => commitMutation,
}))

function TestComponent() {
  const { setOpen } = usePersonsContext()
  return (
    <>
      <Button onClick={() => setOpen('import')}>Open Import</Button>
      <PersonsImportDialog />
    </>
  )
}

describe('PersonsImportDialog', () => {
  it('opens with the CSV upload prompt', async () => {
    const queryClient = new QueryClient()
    const { getByRole, getByText } = await render(
      <QueryClientProvider client={queryClient}>
        <PersonsProvider>
          <TestComponent />
        </PersonsProvider>
      </QueryClientProvider>
    )

    await getByRole('button', { name: 'Open Import' }).click()

    await expect
      .element(getByRole('heading', { name: /批量导入人员/i }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /下载 CSV 模板/i }))
      .toBeInTheDocument()
    await expect.element(getByText(/CSV 格式示例/i)).toBeInTheDocument()
  })
})
