import { useCallback, useMemo, useReducer } from 'react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import {
  EMPTY_CONFIG_JSON,
  MAX_SAMPLE_FPS,
  MIN_SAMPLE_FPS,
  computeInitialDrafts,
  pickActiveAlgo,
  pickNextActiveAfterUncheck,
  resolveDraftConfigJson,
  type AlgoDraftItem,
} from '../data/algo-draft'
import {
  channelTaskDraftReducer,
  type ChannelTaskDraftState,
} from '../data/channel-task-draft-state'
import { fromDraft, serializeRegionConfig } from '../data/region-config'
import { type AlgorithmConfigDraft, type Task } from '../data/schema'
import {
  useRegionEditor,
  type RegionEditorController,
} from './use-region-editor'

export {
  MAX_SAMPLE_FPS,
  MIN_SAMPLE_FPS,
  type AlgoDraftItem,
} from '../data/algo-draft'
export {
  DEFAULT_COMPUTE_COST,
  MAX_COMPUTE_POINTS,
} from './use-task-compute-budget'

/** 无草稿时的稳定空对象：避免每次渲染新建字面量把 useCallback 依赖打穿。 */
const EMPTY_DRAFTS: Record<string, AlgoDraftItem> = {}

export interface ChannelTaskDraftsOptions {
  initialCameraId: string
  initialAlgorithmId?: string
  algorithms: Algorithm[]
  allTasks: Task[]
}

export interface ChannelTaskDraftsController {
  cameraId: string
  channelEnabled: boolean
  setChannelEnabled: (enabled: boolean) => void
  /** 当前相机的算法草稿（区域字段是已提交值，编辑中的区域在 `regionEditor` 里）。 */
  drafts: Record<string, AlgoDraftItem>
  activeAlgoId: string | null
  activeDraft: AlgoDraftItem | null
  regionEditor: RegionEditorController
  /** 切换相机：按相机保留草稿，切回不丢未保存的编辑。 */
  changeCamera: (cameraId: string) => void
  toggleAlgo: (algoId: string, checked: boolean) => void
  selectActiveAlgo: (algoId: string | null) => void
  updateFps: (algoId: string, sampleFps: number) => void
  updateConfigJson: (algoId: string, configJson: string) => void
  /**
   * 生成提交载荷。
   *
   * `regionEditable` 为 false（画面未就绪、禁止改坐标）时**一律沿用已保存的区域**，
   * 绝不把编辑缓冲写进请求 —— 这是 PRD R5 / AC8「无参考画面时不得产生错误坐标」的落点。
   */
  toPayloadDrafts: (options: {
    regionEditable: boolean
  }) => AlgorithmConfigDraft[]
}

function createInitialState(
  initialCameraId: string,
  initialAlgorithmId: string | undefined,
  algorithms: Algorithm[],
  allTasks: Task[]
): ChannelTaskDraftState {
  const drafts = computeInitialDrafts(initialCameraId, allTasks, algorithms)
  const activeAlgoId =
    initialAlgorithmId && drafts[initialAlgorithmId]
      ? initialAlgorithmId
      : pickActiveAlgo(drafts, algorithms)
  return {
    cameraId: initialCameraId,
    enabledByCamera: {
      [initialCameraId]:
        allTasks.find((task) => task.cameraId === initialCameraId)?.enabled ??
        true,
    },
    draftsByCamera: { [initialCameraId]: drafts },
    activeAlgoId,
  }
}

/**
 * 通道任务弹窗的草稿状态。
 *
 * 设计要点（都是踩过的坑）：
 *
 * 1. **区域只有一个编辑缓冲**：`regionEditor` 承载「正在拖拽的那一份」，落进草稿表的
 *    `regionConfigJson` 只作为已提交值。两者在「切换算法 / 切换相机」两个边界显式交接
 *    （见 {@link ChannelTaskDraftAction}），不在渲染期互相 setState。
 * 2. **状态迁移全在纯 reducer 里**：历史上曾用 `setDrafts({...陈旧快照, region})` 整体替换，
 *    同一批里后写的绝对值会把紧挨着的勾选变更覆盖掉——表现为「点算法勾选框没有反应」。
 * 3. **以相机为键保存草稿**：否则切换相机再切回来，未保存的区域与参数编辑会被静默丢弃。
 */
export function useChannelTaskDrafts({
  initialCameraId,
  initialAlgorithmId,
  algorithms,
  allTasks,
}: ChannelTaskDraftsOptions): ChannelTaskDraftsController {
  const [state, dispatch] = useReducer(
    channelTaskDraftReducer,
    { initialCameraId, initialAlgorithmId, algorithms, allTasks },
    (init) =>
      createInitialState(
        init.initialCameraId,
        init.initialAlgorithmId,
        init.algorithms,
        init.allTasks
      )
  )

  const drafts = useMemo(
    () => state.draftsByCamera[state.cameraId] ?? EMPTY_DRAFTS,
    [state.draftsByCamera, state.cameraId]
  )
  const initialRegionJson = state.activeAlgoId
    ? (drafts[state.activeAlgoId]?.regionConfigJson ?? EMPTY_CONFIG_JSON)
    : EMPTY_CONFIG_JSON

  const regionEditor = useRegionEditor(initialRegionJson)

  const currentRegionJson = useCallback(
    () => serializeRegionConfig(fromDraft(regionEditor.draft)),
    [regionEditor.draft]
  )

  const regionJsonFor = useCallback(
    (algoId: string | null) =>
      algoId
        ? (drafts[algoId]?.regionConfigJson ?? EMPTY_CONFIG_JSON)
        : EMPTY_CONFIG_JSON,
    [drafts]
  )

  const selectActiveAlgo = useCallback(
    (algoId: string | null) => {
      if (algoId === state.activeAlgoId) return
      dispatch({
        type: 'select-active',
        algoId,
        currentRegionJson: currentRegionJson(),
      })
      regionEditor.reset(regionJsonFor(algoId))
    },
    [state.activeAlgoId, currentRegionJson, regionJsonFor, regionEditor]
  )

  const toggleAlgo = useCallback(
    (algoId: string, checked: boolean) => {
      const nextActiveAlgoId = checked
        ? algoId
        : state.activeAlgoId === algoId
          ? pickNextActiveAfterUncheck(drafts, algoId)
          : state.activeAlgoId
      const algorithmsById = new Map(algorithms.map((algo) => [algo.id, algo]))

      dispatch({
        type: 'toggle-algo',
        algoId,
        checked,
        configJson: resolveDraftConfigJson(
          algorithmsById.get(algoId),
          drafts[algoId]?.configJson
        ),
        currentRegionJson: currentRegionJson(),
        nextActiveAlgoId,
      })

      if (nextActiveAlgoId !== state.activeAlgoId) {
        regionEditor.reset(regionJsonFor(nextActiveAlgoId))
      }
    },
    [
      algorithms,
      drafts,
      state.activeAlgoId,
      currentRegionJson,
      regionJsonFor,
      regionEditor,
    ]
  )

  const changeCamera = useCallback(
    (cameraId: string) => {
      if (cameraId === state.cameraId) return
      const draftsForCamera =
        state.draftsByCamera[cameraId] ??
        computeInitialDrafts(cameraId, allTasks, algorithms)
      const activeAlgoId = pickActiveAlgo(draftsForCamera, algorithms)

      dispatch({
        type: 'change-camera',
        cameraId,
        drafts: draftsForCamera,
        activeAlgoId,
        currentRegionJson: currentRegionJson(),
        channelEnabled:
          state.enabledByCamera[cameraId] ??
          allTasks.find((task) => task.cameraId === cameraId)?.enabled ??
          true,
      })
      regionEditor.reset(
        activeAlgoId
          ? (draftsForCamera[activeAlgoId]?.regionConfigJson ??
              EMPTY_CONFIG_JSON)
          : EMPTY_CONFIG_JSON
      )
    },
    [
      state.cameraId,
      state.draftsByCamera,
      state.enabledByCamera,
      allTasks,
      algorithms,
      currentRegionJson,
      regionEditor,
    ]
  )

  const updateFps = useCallback((algoId: string, sampleFps: number) => {
    dispatch({
      type: 'update-fps',
      algoId,
      sampleFps: Math.max(
        MIN_SAMPLE_FPS,
        Math.min(MAX_SAMPLE_FPS, Math.round(sampleFps))
      ),
    })
  }, [])

  const updateConfigJson = useCallback((algoId: string, configJson: string) => {
    dispatch({ type: 'update-config-json', algoId, configJson })
  }, [])

  const setChannelEnabled = useCallback((enabled: boolean) => {
    dispatch({ type: 'set-channel-enabled', enabled })
  }, [])

  const toPayloadDrafts = useCallback(
    ({ regionEditable }: { regionEditable: boolean }) => {
      const editedRegion = regionEditable ? currentRegionJson() : null
      return Object.entries(drafts)
        .filter(([, draft]) => draft.selected)
        .map<AlgorithmConfigDraft>(([algoId, draft]) => ({
          algorithmId: algoId,
          sampleFps: draft.sampleFps,
          enabled: draft.enabled,
          configJson: draft.configJson?.trim()
            ? draft.configJson
            : EMPTY_CONFIG_JSON,
          regionConfigJson:
            algoId === state.activeAlgoId && editedRegion !== null
              ? editedRegion
              : draft.regionConfigJson,
        }))
    },
    [drafts, state.activeAlgoId, currentRegionJson]
  )

  const activeAlgoId = state.activeAlgoId

  return {
    cameraId: state.cameraId,
    channelEnabled: state.enabledByCamera[state.cameraId] ?? true,
    setChannelEnabled,
    drafts,
    activeAlgoId,
    activeDraft: activeAlgoId ? (drafts[activeAlgoId] ?? null) : null,
    regionEditor,
    changeCamera,
    toggleAlgo,
    selectActiveAlgo,
    updateFps,
    updateConfigJson,
    toPayloadDrafts,
  }
}
