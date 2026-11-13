import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { personsApi } from '../api/persons-api'
import { useCreateFaceImportJob, useExportPersonsManifest } from './use-persons'

vi.mock('../api/persons-api', () => ({
  personsApi: {
    createFaceImportJob: vi.fn(),
    exportManifest: vi.fn(),
  },
}))

describe('use-persons mutation global toast suppression', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('marks useCreateFaceImportJob and useExportPersonsManifest with suppressGlobalToast so MutationCache does not double-toast', async () => {
    const globalErrorHandler = vi.fn()
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
      mutationCache: new MutationCache({
        onError: (error, _v, _c, mutation) => {
          if (mutation.meta?.suppressGlobalToast) return
          globalErrorHandler(error)
        },
      }),
    })

    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    vi.mocked(personsApi.createFaceImportJob).mockRejectedValueOnce(
      new Error('zip bad')
    )
    vi.mocked(personsApi.exportManifest).mockRejectedValueOnce(
      new Error('export failed')
    )

    const { result: importHook } = await renderHook(
      () => useCreateFaceImportJob(),
      { wrapper }
    )
    const { result: exportHook } = await renderHook(
      () => useExportPersonsManifest(),
      { wrapper }
    )

    await expect(
      importHook.current.mutateAsync(
        new File(['x'], 'a.zip', { type: 'application/zip' })
      )
    ).rejects.toThrow('zip bad')

    await expect(exportHook.current.mutateAsync({})).rejects.toThrow(
      'export failed'
    )

    expect(globalErrorHandler).not.toHaveBeenCalled()
  })
})
