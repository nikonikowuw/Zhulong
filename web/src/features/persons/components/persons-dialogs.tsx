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

const PersonsFaceReextractDialog = lazy(() =>
  import('./persons-face-reextract-dialog').then((m) => ({
    default: m.PersonsFaceReextractDialog,
  }))
)

export function PersonsDialogs() {
  const { open, previewImage } = usePersonsState()

  return (
    <>
      {(open === 'create' || open === 'edit') && <PersonsActionDialog />}
      {open === 'faces' && <PersonsFacesDialog />}
      {open === 'delete' && <PersonsDeleteDialog />}
      {Boolean(previewImage) && <PersonsImagePreviewDialog />}

      <Suspense fallback={null}>
        {open === 'import' && <PersonsImportDialog />}
        {open === 'import-faces' && <PersonsFaceImportDialog />}
        {open === 'reextract-faces' && <PersonsFaceReextractDialog />}
      </Suspense>
    </>
  )
}
