import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type AuditLog } from '../data/schema'

interface AuditContextType {
  selectedLog: AuditLog | null
  setSelectedLog: (log: AuditLog | null) => void
  isDetailOpen: boolean
  setIsDetailOpen: (open: boolean) => void
  openDetail: (log: AuditLog) => void
}

const AuditContext = React.createContext<AuditContextType | null>(null)

export function AuditProvider({ children }: { children: React.ReactNode }) {
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null)
  const [activeDialog, setActiveDialog] = useDialogState<'detail'>()
  const isDetailOpen = activeDialog === 'detail'
  const setIsDetailOpen = (open: boolean) =>
    setActiveDialog(open ? 'detail' : null)

  const openDetail = (log: AuditLog) => {
    setSelectedLog(log)
    setActiveDialog('detail')
  }

  return (
    <AuditContext
      value={{
        selectedLog,
        setSelectedLog,
        isDetailOpen,
        setIsDetailOpen,
        openDetail,
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
