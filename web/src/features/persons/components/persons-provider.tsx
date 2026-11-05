import React, { createContext, useContext, useMemo, useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Person } from '../data/schema'

export type PersonsDialogType =
  | 'create'
  | 'edit'
  | 'delete'
  | 'faces'
  | 'import'
  | 'import-faces'

export interface PreviewImageInfo {
  url: string
  title: string
}

export interface PersonsStateContextType {
  open: PersonsDialogType | null
  currentRow: Person | null
  previewImage: PreviewImageInfo | null
}

export interface PersonsActionsContextType {
  setOpen: (str: PersonsDialogType | null) => void
  setCurrentRow: React.Dispatch<React.SetStateAction<Person | null>>
  setPreviewImage: React.Dispatch<React.SetStateAction<PreviewImageInfo | null>>
}

export interface PersonsContextType
  extends PersonsStateContextType, PersonsActionsContextType {}

const PersonsStateContext = createContext<PersonsStateContextType | null>(null)
const PersonsActionsContext = createContext<PersonsActionsContextType | null>(
  null
)

export function PersonsProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<PersonsDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Person | null>(null)
  const [previewImage, setPreviewImage] = useState<PreviewImageInfo | null>(
    null
  )

  const stateValue = useMemo<PersonsStateContextType>(
    () => ({
      open,
      currentRow,
      previewImage,
    }),
    [open, currentRow, previewImage]
  )

  const actionsValue = useMemo<PersonsActionsContextType>(
    () => ({
      setOpen,
      setCurrentRow,
      setPreviewImage,
    }),
    [setOpen]
  )

  return (
    <PersonsActionsContext.Provider value={actionsValue}>
      <PersonsStateContext.Provider value={stateValue}>
        {children}
      </PersonsStateContext.Provider>
    </PersonsActionsContext.Provider>
  )
}

/** 仅订阅动作调度（引用恒定，弹窗切换或行变化时绝不触发重渲染） */
// eslint-disable-next-line react-refresh/only-export-components
export const usePersonsActions = () => {
  const context = useContext(PersonsActionsContext)
  if (!context) {
    throw new Error('usePersonsActions must be used within <PersonsProvider>')
  }
  return context
}

/** 仅订阅瞬态状态变化 */
// eslint-disable-next-line react-refresh/only-export-components
export const usePersonsState = () => {
  const context = useContext(PersonsStateContext)
  if (!context) {
    throw new Error('usePersonsState must be used within <PersonsProvider>')
  }
  return context
}

/** 全量合并订阅（向后兼容） */
// eslint-disable-next-line react-refresh/only-export-components
export const usePersonsContext = (): PersonsContextType => {
  const state = usePersonsState()
  const actions = usePersonsActions()

  return useMemo(
    () => ({
      ...state,
      ...actions,
    }),
    [state, actions]
  )
}
