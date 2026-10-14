import { PersonsActionDialog } from './persons-action-dialog'
import { PersonsDeleteDialog } from './persons-delete-dialog'
import { PersonsFacesDialog } from './persons-faces-dialog'
import { PersonsImagePreviewDialog } from './persons-image-preview-dialog'

export function PersonsDialogs() {
  return (
    <>
      <PersonsActionDialog />
      <PersonsFacesDialog />
      <PersonsDeleteDialog />
      <PersonsImagePreviewDialog />
    </>
  )
}
