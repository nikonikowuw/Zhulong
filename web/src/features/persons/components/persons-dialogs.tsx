import { PersonsActionDialog } from './persons-action-dialog'
import { PersonsDeleteDialog } from './persons-delete-dialog'
import { PersonsFaceImportDialog } from './persons-face-import-dialog'
import { PersonsFacesDialog } from './persons-faces-dialog'
import { PersonsImagePreviewDialog } from './persons-image-preview-dialog'
import { PersonsImportDialog } from './persons-import-dialog'

export function PersonsDialogs() {
  return (
    <>
      <PersonsActionDialog />
      <PersonsFacesDialog />
      <PersonsDeleteDialog />
      <PersonsImagePreviewDialog />
      <PersonsImportDialog />
      <PersonsFaceImportDialog />
    </>
  )
}
