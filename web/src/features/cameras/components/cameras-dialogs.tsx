import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasDeleteDialog } from './cameras-delete-dialog'
import { CamerasDiagnoseDialog } from './cameras-diagnose-dialog'

export function CamerasDialogs() {
  return (
    <>
      <CamerasActionDialog />
      <CamerasDiagnoseDialog />
      <CamerasDeleteDialog />
    </>
  )
}
