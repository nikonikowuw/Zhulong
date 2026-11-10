import React, { createContext, useContext, useMemo, useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Person } from '../data/schema'

type PersonsDialogType =
  | 'create'
  | 'edit'
  | 'delete'
  | 'faces'
  | 'import'
  | 'import-faces'

interface PreviewImageInfo {
  url: string
  title: string
}

interface PersonsStateContextType {
  open: PersonsDialogType | null
  currentRow: Person | null
  previewImage: PreviewImageInfo | null
}

interface PersonsActionsContextType {
  setOpen: (str: PersonsDialogType | null) => void
  setCurrentRow: React.Dispatch<React.SetStateAction<Person | null>>
  setPreviewImage: React.Dispatch<React.SetStateAction<PreviewImageInfo | null>>
}

interface PersonsContextType
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
    () => ({ open, currentRow, previewImage }),
    [open, currentRow, previewImage]
  )

  // 三个 setter 全部引用恒定（`useDialogState` 用 `useCallback`，`useState` 的
  // setter 天然稳定），因此这个对象在整个 Provider 生命周期内只创建一次 ——
  // 弹窗切换与行变化都不会让「只订阅动作」的消费者重渲染。曾经的依赖数组写的是
  // `[setOpen]`，而当时的 `setOpen` 每次渲染都新建，等于没有 memo。
  const actionsValue = useMemo<PersonsActionsContextType>(
    () => ({ setOpen, setCurrentRow, setPreviewImage }),
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

  return useMemo(() => ({ ...state, ...actions }), [state, actions])
}
