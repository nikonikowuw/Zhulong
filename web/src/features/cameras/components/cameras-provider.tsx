import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Camera } from '../data/schema'

type CamerasDialogType =
  | 'create'
  | 'edit'
  | 'delete'
  | 'multi-delete'
  | 'diagnose'

interface CamerasContextType {
  open: CamerasDialogType | null
  setOpen: (str: CamerasDialogType | null) => void
  currentRow: Camera | null
  setCurrentRow: React.Dispatch<React.SetStateAction<Camera | null>>
}

const CamerasContext = React.createContext<CamerasContextType | null>(null)

export function CamerasProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<CamerasDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Camera | null>(null)

  return (
    <CamerasContext value={{ open, setOpen, currentRow, setCurrentRow }}>
      {children}
    </CamerasContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useCamerasContext = () => {
  const context = React.useContext(CamerasContext)
  if (!context) {
    throw new Error('useCamerasContext must be used within <CamerasProvider>')
  }
  return context
}
