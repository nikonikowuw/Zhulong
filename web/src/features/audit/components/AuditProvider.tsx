import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type AuditLog } from '../data/schema'

interface AuditContextType {
  selectedLog: AuditLog | null
  setSelectedLog: (log: AuditLog | null) => void
  isDetailOpen: boolean
  setIsDetailOpen: (open: boolean) => void
  isClearOpen: boolean
  setIsClearOpen: (open: boolean) => void
  openDetail: (log: AuditLog) => void
  openClear: () => void
}

const AuditContext = React.createContext<AuditContextType | null>(null)

export function AuditProvider({ children }: { children: React.ReactNode }) {
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null)
  const [activeDialog, setActiveDialog] = useDialogState<'detail' | 'clear'>()
  const isDetailOpen = activeDialog === 'detail'
  const isClearOpen = activeDialog === 'clear'
  const setIsDetailOpen = (open: boolean) =>
    setActiveDialog(open ? 'detail' : null)
  const setIsClearOpen = (open: boolean) =>
    setActiveDialog(open ? 'clear' : null)

  const openDetail = (log: AuditLog) => {
    setSelectedLog(log)
    setActiveDialog('detail')
  }

  const openClear = () => {
    setActiveDialog('clear')
  }

  return (
    <AuditContext
      value={{
        selectedLog,
        setSelectedLog,
        isDetailOpen,
        setIsDetailOpen,
        isClearOpen,
        setIsClearOpen,
        openDetail,
        openClear,
      }}
    >
      {children}
    </AuditContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useAuditContext = () => {
  const context = React.useContext(AuditContext)
  if (!context) {
    throw new Error('useAuditContext must be used within <AuditProvider>')
  }
  return context
}
