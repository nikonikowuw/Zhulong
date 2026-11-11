import React, { createContext, useContext, useMemo, useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Camera } from '../data/schema'

export type CamerasDialogType =
  | 'create'
  | 'edit'
  | 'delete'
  | 'multi-delete'
  | 'diagnose'

export interface CamerasStateContextType {
  open: CamerasDialogType | null
  currentRow: Camera | null
}

export interface CamerasActionsContextType {
  setOpen: (str: CamerasDialogType | null) => void
  setCurrentRow: React.Dispatch<React.SetStateAction<Camera | null>>
}

export interface CamerasContextType
  extends CamerasStateContextType, CamerasActionsContextType {}

const CamerasStateContext = createContext<CamerasStateContextType | null>(null)
const CamerasActionsContext = createContext<CamerasActionsContextType | null>(
  null
)

export function CamerasProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<CamerasDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Camera | null>(null)

  const stateValue = useMemo<CamerasStateContextType>(
    () => ({
      open,
      currentRow,
    }),
    [open, currentRow]
  )

  const actionsValue = useMemo<CamerasActionsContextType>(
    () => ({
      setOpen,
      setCurrentRow,
    }),
    [setOpen]
  )

  return (
    <CamerasActionsContext value={actionsValue}>
      <CamerasStateContext value={stateValue}>{children}</CamerasStateContext>
    </CamerasActionsContext>
  )
}

/** 仅订阅动作调度（引用恒定，弹窗切换或行变化时绝不触发重渲染） */
// eslint-disable-next-line react-refresh/only-export-components
export const useCamerasActions = () => {
  const context = useContext(CamerasActionsContext)
  if (!context) {
    throw new Error('useCamerasActions must be used within <CamerasProvider>')
  }
  return context
}

/** 仅订阅瞬态状态变化 */
// eslint-disable-next-line react-refresh/only-export-components
export const useCamerasState = () => {
  const context = useContext(CamerasStateContext)
  if (!context) {
    throw new Error('useCamerasState must be used within <CamerasProvider>')
  }
  return context
}

/** 全量合并订阅（向后兼容） */
// eslint-disable-next-line react-refresh/only-export-components
export const useCamerasContext = (): CamerasContextType => {
  const state = useCamerasState()
  const actions = useCamerasActions()

  return useMemo(
    () => ({
      ...state,
      ...actions,
    }),
    [state, actions]
  )
}
