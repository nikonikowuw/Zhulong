import { PersonsActionDialog } from './persons-action-dialog'
import { PersonsDeleteDialog } from './persons-delete-dialog'
import { PersonsFacesDialog } from './persons-faces-dialog'

export function PersonsDialogs() {
  return (
    <>
      <PersonsActionDialog />
      <PersonsFacesDialog />
      <PersonsDeleteDialog />
    </>
  )
}
