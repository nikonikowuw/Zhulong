import { lazy, Suspense } from 'react'
import { PersonsActionDialog } from './persons-action-dialog'
import { PersonsDeleteDialog } from './persons-delete-dialog'
import { PersonsFacesDialog } from './persons-faces-dialog'
import { PersonsImagePreviewDialog } from './persons-image-preview-dialog'
import { usePersonsState } from './persons-provider'

const PersonsFaceImportDialog = lazy(() =>
  import('./persons-face-import-dialog').then((m) => ({
    default: m.PersonsFaceImportDialog,
  }))
)

const PersonsImportDialog = lazy(() =>
  import('./persons-import-dialog').then((m) => ({
    default: m.PersonsImportDialog,
  }))
)

export function PersonsDialogs() {
  const { open } = usePersonsState()

  return (
    <>
      <PersonsActionDialog />
      <PersonsFacesDialog />
      <PersonsDeleteDialog />
      <PersonsImagePreviewDialog />

      <Suspense fallback={null}>
        {open === 'import' && <PersonsImportDialog />}
        {open === 'import-faces' && <PersonsFaceImportDialog />}
      </Suspense>
    </>
  )
}
