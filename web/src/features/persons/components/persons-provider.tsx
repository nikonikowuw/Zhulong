import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useQueryClient } from '@tanstack/react-query'
import useDialogState from '@/hooks/use-dialog-state'
import { type Person } from '../data/schema'
import {
  LATEST_FACE_REEXTRACT_JOB_QUERY_KEY,
  PERSONS_QUERY_KEY,
  useFaceImportJob,
  useFaceReextractJob,
} from '../hooks/use-persons'

type PersonsDialogType =
  | 'create'
  | 'edit'
  | 'delete'
  | 'faces'
  | 'import'
  | 'import-faces'
  | 'reextract-faces'

export interface ReextractTarget {
  personIds: string[]
  label?: string
}

interface PreviewImageInfo {
  url: string
  title: string
}

interface PersonsStateContextType {
  open: PersonsDialogType | null
  currentRow: Person | null
  previewImage: PreviewImageInfo | null
  /**
   * 人脸批量导入任务 id。
   *
   * 放在 Provider 而不是弹窗里：弹窗是 `lazy` + 条件挂载，关闭即卸载。若 id
   * 存在弹窗内部，「后台运行」就是一句假话 —— 组件一卸载，轮询停了，任务完成
   * 后也没人刷新列表。挂到全局后，弹窗关闭期间轮询与失效照常进行。
   */
  faceImportJobId: string | null
  faceReextractJobId: string | null
  isFaceReextractRunning: boolean
  reextractTarget: ReextractTarget | null
}

interface PersonsActionsContextType {
  setOpen: (str: PersonsDialogType | null) => void
  setCurrentRow: React.Dispatch<React.SetStateAction<Person | null>>
  setPreviewImage: React.Dispatch<React.SetStateAction<PreviewImageInfo | null>>
  setFaceImportJobId: React.Dispatch<React.SetStateAction<string | null>>
  setFaceReextractJobId: React.Dispatch<React.SetStateAction<string | null>>
  setReextractTarget: React.Dispatch<
    React.SetStateAction<ReextractTarget | null>
  >
  openFaceReextract: (target: ReextractTarget | null) => void
}

interface PersonsContextType
  extends PersonsStateContextType, PersonsActionsContextType {}

const PersonsStateContext = createContext<PersonsStateContextType | null>(null)
const PersonsActionsContext = createContext<PersonsActionsContextType | null>(
  null
)
const PersonsReextractRunningContext = createContext<boolean>(false)

export function PersonsProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useDialogState<PersonsDialogType>(null)
  const [currentRow, setCurrentRow] = useState<Person | null>(null)
  const [previewImage, setPreviewImage] = useState<PreviewImageInfo | null>(
    null
  )
  const [faceImportJobId, setFaceImportJobId] = useState<string | null>(null)
  const [faceReextractJobId, setFaceReextractJobId] = useState<string | null>(
    null
  )
  const [reextractTarget, setReextractTarget] =
    useState<ReextractTarget | null>(null)

  // 轮询与「任务完成后刷新列表」都在这里跑，不依赖弹窗是否挂载。
  const queryClient = useQueryClient()
  const jobQuery = useFaceImportJob(faceImportJobId, true)
  const jobStatus = jobQuery.data?.status
  useEffect(() => {
    if (jobStatus === 'COMPLETED') {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    }
  }, [jobStatus, queryClient])

  const reextractJobQuery = useFaceReextractJob(faceReextractJobId, true)
  const reextractStatus = reextractJobQuery.data?.status
  const isFaceReextractRunning = Boolean(
    faceReextractJobId &&
    (!reextractStatus ||
      reextractStatus === 'PENDING' ||
      reextractStatus === 'EXTRACTING' ||
      reextractStatus === 'PROCESSING')
  )
  const faceReextractJobIdRef = useRef(faceReextractJobId)
  useEffect(() => {
    faceReextractJobIdRef.current = faceReextractJobId
  }, [faceReextractJobId])

  const openFaceReextract = useCallback(
    (target: ReextractTarget | null) => {
      if (!faceReextractJobIdRef.current) {
        setReextractTarget(target)
      }
      setOpen('reextract-faces')
    },
    [setOpen]
  )

  useEffect(() => {
    if (reextractStatus === 'COMPLETED' || reextractStatus === 'CANCELLED') {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    }
    if (
      reextractStatus === 'COMPLETED' ||
      reextractStatus === 'CANCELLED' ||
      reextractStatus === 'FAILED'
    ) {
      void queryClient.invalidateQueries({
        queryKey: LATEST_FACE_REEXTRACT_JOB_QUERY_KEY,
      })
    }
  }, [reextractStatus, queryClient])

  const stateValue = useMemo<PersonsStateContextType>(
    () => ({
      open,
      currentRow,
      previewImage,
      faceImportJobId,
      faceReextractJobId,
      isFaceReextractRunning,
      reextractTarget,
    }),
    [
      open,
      currentRow,
      previewImage,
      faceImportJobId,
      faceReextractJobId,
      isFaceReextractRunning,
      reextractTarget,
    ]
  )

  // 所有 setter 引用恒定（`useDialogState` 用 `useCallback`，`useState` 的
  // setter 天然稳定），因此这个对象在整个 Provider 生命周期内只创建一次 ——
  // 弹窗切换与行变化都不会让「只订阅动作」的消费者重渲染。
  const actionsValue = useMemo<PersonsActionsContextType>(
    () => ({
      setOpen,
      setCurrentRow,
      setPreviewImage,
      setFaceImportJobId,
      setFaceReextractJobId,
      setReextractTarget,
      openFaceReextract,
    }),
    [setOpen, openFaceReextract]
  )

  return (
    <PersonsActionsContext.Provider value={actionsValue}>
      <PersonsReextractRunningContext.Provider value={isFaceReextractRunning}>
        <PersonsStateContext.Provider value={stateValue}>
          {children}
        </PersonsStateContext.Provider>
      </PersonsReextractRunningContext.Provider>
    </PersonsActionsContext.Provider>
  )
}

/** 仅订阅特征重提任务是否运行中（避免工具栏因弹窗/行切换重渲染） */
// eslint-disable-next-line react-refresh/only-export-components
export const useIsFaceReextractRunning = () =>
  useContext(PersonsReextractRunningContext)

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
