import React, { useState } from 'react'
import useDialogState from '@/hooks/use-dialog-state'
import { type Task } from '../data/schema'

export type TasksDialogType = 'channelTask' | 'delete'

interface TasksContextType {
  open: TasksDialogType | null
  setOpen: (str: TasksDialogType | null) => void
  /** 当前操作行（编辑 / 删除二次确认） */
  currentRow: Task | null
  setCurrentRow: React.Dispatch<React.SetStateAction<Task | null>>
  /** 未绑定任务的通道预选（通道卡片「配置算法」入口） */
  targetCameraId: string | null
  setTargetCameraId: React.Dispatch<React.SetStateAction<string | null>>
  /** 从特定算法订阅实例触发编辑时的预激活算法 ID */
  targetAlgorithmId: string | null
  setTargetAlgorithmId: React.Dispatch<React.SetStateAction<string | null>>
}

const TasksContext = React.createContext<TasksContextType | null>(null)

export function TasksProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<TasksDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Task | null>(null)
  const [targetCameraId, setTargetCameraId] = useState<string | null>(null)
  const [targetAlgorithmId, setTargetAlgorithmId] = useState<string | null>(
    null
  )

  return (
    <TasksContext
      value={{
        open,
        setOpen,
        currentRow,
        setCurrentRow,
        targetCameraId,
        setTargetCameraId,
        targetAlgorithmId,
        setTargetAlgorithmId,
      }}
    >
      {children}
    </TasksContext>
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
