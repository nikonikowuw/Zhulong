import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Person } from '../data/schema'

export type PersonsDialogType = 'create' | 'edit' | 'delete' | 'faces'

export interface PreviewImageInfo {
  url: string
  title: string
}

interface PersonsContextType {
  open: PersonsDialogType | null
  setOpen: (str: PersonsDialogType | null) => void
  currentRow: Person | null
  setCurrentRow: React.Dispatch<React.SetStateAction<Person | null>>
  previewImage: PreviewImageInfo | null
  setPreviewImage: React.Dispatch<React.SetStateAction<PreviewImageInfo | null>>
}

const PersonsContext = React.createContext<PersonsContextType | null>(null)

export function PersonsProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<PersonsDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Person | null>(null)
  const [previewImage, setPreviewImage] = useState<PreviewImageInfo | null>(null)

  return (
    <PersonsContext
      value={{
        open,
        setOpen,
        currentRow,
        setCurrentRow,
        previewImage,
        setPreviewImage,
      }}
    >
      {children}
    </PersonsContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const usePersonsContext = () => {
  const context = React.useContext(PersonsContext)
  if (!context) {
    throw new Error('usePersonsContext must be used within <PersonsProvider>')
  }
  return context
}
