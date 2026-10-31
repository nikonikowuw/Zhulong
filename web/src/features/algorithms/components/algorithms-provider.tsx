import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Algorithm } from '../data/schema'

export type AlgorithmsDialogType =
  | 'upload'
  | 'delete'
  | 'reset'
  | 'trial'
  | 'detail'

interface AlgorithmsContextType {
  open: AlgorithmsDialogType | null
  setOpen: (dialog: AlgorithmsDialogType | null) => void
  currentRow: Algorithm | null
  setCurrentRow: React.Dispatch<React.SetStateAction<Algorithm | null>>
}

const AlgorithmsContext = React.createContext<AlgorithmsContextType | null>(
  null
)

export function AlgorithmsProvider({
  children,
}: {
  children: React.ReactNode
}) {
  const [open, setOpen] = useDialogState<AlgorithmsDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Algorithm | null>(null)

  return (
    <AlgorithmsContext.Provider
      value={{ open, setOpen, currentRow, setCurrentRow }}
    >
      {children}
    </AlgorithmsContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useAlgorithmsContext = () => {
  const context = React.useContext(AlgorithmsContext)
  if (!context) {
    throw new Error(
      'useAlgorithmsContext must be used within <AlgorithmsProvider>'
    )
  }
  return context
}
