import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Task } from '../data/schema'

export type TasksDialogType = 'channelTask' | 'delete'

interface TasksActionsContextType {
  setOpen: (str: TasksDialogType | null) => void
  setCurrentRow: React.Dispatch<React.SetStateAction<Task | null>>
  setTargetCameraId: React.Dispatch<React.SetStateAction<string | null>>
  setTargetAlgorithmId: React.Dispatch<React.SetStateAction<string | null>>
}

interface TasksContextType extends TasksActionsContextType {
  open: TasksDialogType | null
  /** 当前操作行（编辑 / 删除二次确认） */
  currentRow: Task | null
  /** 未绑定任务的通道预选（通道卡片「配置算法」入口） */
  targetCameraId: string | null
  /** 从特定算法订阅实例触发编辑时的预激活算法 ID */
  targetAlgorithmId: string | null
}

const TasksContext = React.createContext<TasksContextType | null>(null)
const TasksActionsContext = React.createContext<TasksActionsContextType | null>(
  null
)

export function TasksProvider({ children }: { children: React.ReactNode }) {
  const [open, rawSetOpen] = useDialogState<TasksDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Task | null>(null)
  const [targetCameraId, setTargetCameraId] = useState<string | null>(null)
  const [targetAlgorithmId, setTargetAlgorithmId] = useState<string | null>(
    null
  )

  const setOpenRef = useRef(rawSetOpen)
  useEffect(() => {
    setOpenRef.current = rawSetOpen
  }, [rawSetOpen])

  const setOpen = useCallback((str: TasksDialogType | null) => {
    setOpenRef.current(str)
  }, [])

  const actions = useMemo<TasksActionsContextType>(
    () => ({
      setOpen,
      setCurrentRow,
      setTargetCameraId,
      setTargetAlgorithmId,
    }),
    [setOpen]
  )

  const value = useMemo<TasksContextType>(
    () => ({
      open,
      setOpen,
      currentRow,
      setCurrentRow,
      targetCameraId,
      setTargetCameraId,
      targetAlgorithmId,
      setTargetAlgorithmId,
    }),
    [open, setOpen, currentRow, targetCameraId, targetAlgorithmId]
  )

  return (
    <TasksActionsContext value={actions}>
      <TasksContext value={value}>{children}</TasksContext>
    </TasksActionsContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useTasksContext = () => {
  const context = React.useContext(TasksContext)
  if (!context) {
    throw new Error('useTasksContext must be used within <TasksProvider>')
  }
  return context
}

// eslint-disable-next-line react-refresh/only-export-components
export const useTasksActions = () => {
  const context = React.useContext(TasksActionsContext)
  if (!context) {
    throw new Error('useTasksActions must be used within <TasksProvider>')
  }
  return context
}
