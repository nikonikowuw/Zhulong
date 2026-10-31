import { AlgorithmDeleteDialog } from './algorithm-delete-dialog'
import { AlgorithmResetDialog } from './algorithm-reset-dialog'
import { AlgorithmUploadDialog } from './algorithm-upload-dialog'
import { useAlgorithmsContext } from './algorithms-provider'

export function AlgorithmsDialogs(): React.JSX.Element {
  const { open, setOpen, currentRow, setCurrentRow } = useAlgorithmsContext()

  return (
    <>
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
    </>
  )
}
