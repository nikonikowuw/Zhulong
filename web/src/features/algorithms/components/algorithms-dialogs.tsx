import { AlgorithmDeleteDialog } from './algorithm-delete-dialog'
import { AlgorithmDetailSheet } from './algorithm-detail-sheet'
import { AlgorithmResetDialog } from './algorithm-reset-dialog'
import { AlgorithmTrialDialog } from './algorithm-trial-dialog'
import { AlgorithmUploadDialog } from './algorithm-upload-dialog'
import { useAlgorithmsContext } from './algorithms-provider'

export function AlgorithmsDialogs(): React.JSX.Element {
  const { open, setOpen, currentRow, setCurrentRow } = useAlgorithmsContext()

  return (
    <>
      <AlgorithmDetailSheet
        algorithm={currentRow}
        open={open === 'detail'}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            setOpen(null)
            setCurrentRow(null)
          }
        }}
        onOpenTrial={(algo) => {
          setCurrentRow(algo)
          setOpen('trial')
        }}
      />

      <AlgorithmUploadDialog
        open={open === 'upload'}
        onOpenChange={(isOpen) => {
          if (!isOpen) setOpen(null)
        }}
      />

      <AlgorithmDeleteDialog
        algorithm={currentRow}
        open={open === 'delete'}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            setOpen(null)
            setCurrentRow(null)
          }
        }}
      />

      <AlgorithmResetDialog
        algorithm={currentRow}
        open={open === 'reset'}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            setOpen(null)
            setCurrentRow(null)
          }
        }}
      />

      <AlgorithmTrialDialog
        algorithm={currentRow}
        open={open === 'trial'}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            setOpen(null)
            setCurrentRow(null)
          }
        }}
      />
    </>
  )
}
