import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { PersonsFaceImportDialog } from './persons-face-import-dialog'
import { PersonsProvider, usePersonsContext } from './persons-provider'

/**
 * 「后台运行」的语义回归。
 *
 * 弹窗是 `lazy` + 条件挂载的：只要任务 id 存在弹窗内部的 `useState` 里，
 * 关闭弹窗就会卸载组件、停掉轮询，「后台继续运行」变成一句假话。任务 id 必须
 * 挂在 Provider 上，关闭再打开还能拿回同一个任务。
 */

const jobId = 'job-42'

vi.mock('../hooks/use-persons', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/use-persons')>()
  return {
    ...actual,
    useCreateFaceImportJob: () => ({
      mutateAsync: vi.fn().mockResolvedValue({ jobId }),
      isPending: false,
    }),
    useFaceImportJob: (id: string | null) => ({
      data: id
        ? {
            jobId: id,
            status: 'PROCESSING',
            progress: 40,
            total: 10,
            processed: 4,
            succeeded: 3,
            failed: 1,
            createdAt: '2026-10-14T00:00:00Z',
            updatedAt: '2026-10-14T00:00:00Z',
          }
        : null,
      isError: false,
      refetch: vi.fn(),
    }),
  }
})

function Harness() {
  const { open, setOpen, setFaceImportJobId, faceImportJobId } =
    usePersonsContext()
  return (
    <div>
      <span data-testid='job-id'>{faceImportJobId ?? 'none'}</span>
      <Button onClick={() => setFaceImportJobId(jobId)}>Attach job</Button>
      <Button onClick={() => setOpen('import-faces')}>Open</Button>
      <Button onClick={() => setOpen(null)}>Close</Button>
      {open === 'import-faces' && <PersonsFaceImportDialog />}
    </div>
  )
}

function setup() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <PersonsProvider>
        <Harness />
      </PersonsProvider>
    </QueryClientProvider>
  )
}

describe('face import job survives dialog unmount', () => {
  it('keeps the job id in the provider after closing the dialog', async () => {
    const screen = await setup()

    await screen.getByRole('button', { name: 'Attach job' }).click()
    await expect.element(screen.getByTestId('job-id')).toHaveTextContent(jobId)

    await screen.getByRole('button', { name: 'Open' }).click()
    await screen.getByRole('button', { name: /完成|确定|稍后|后台/ }).click()

    // 弹窗卸载后任务 id 仍在：轮询由 Provider 承担，不受挂载态影响。
    await expect.element(screen.getByTestId('job-id')).toHaveTextContent(jobId)
    await expect
      .element(screen.getByRole('button', { name: 'Open' }))
      .toBeInTheDocument()
  })
})
