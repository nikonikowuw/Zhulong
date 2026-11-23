import { CamerasActionDialog } from './cameras-action-dialog'
import { CamerasDeleteDialog } from './cameras-delete-dialog'
import { CamerasDiagnoseDialog } from './cameras-diagnose-dialog'
import { CamerasDiscoverDialog } from './cameras-discover-dialog'

export function CamerasDialogs() {
  return (
    <>
      <CamerasActionDialog />
      <CamerasDiagnoseDialog />
      <CamerasDeleteDialog />
      <CamerasDiscoverDialog />
    </>
  )
}
