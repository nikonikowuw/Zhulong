import { createDraftItem, type AlgoDraftItem } from './algo-draft'
import { type Rect } from './region-config'

/** 通道任务弹窗的草稿状态。按相机分组，切回相机时未保存的编辑仍在。 */
export interface ChannelTaskDraftState {
  cameraId: string
  enabledByCamera: Record<string, boolean>
  cropByCamera: Record<string, Rect | null>
  draftsByCamera: Record<string, Record<string, AlgoDraftItem>>
  activeAlgoId: string | null
}

/**
 * 状态迁移动作。
 *
 * 需要「区域交接」的动作都显式携带 `currentRegionJson`：本状态机不持有编辑缓冲，
 * 编辑缓冲的归属由 {@link useChannelTaskDrafts} 决定。这样区域写回只有一条路径，
 * 不会出现两处真源互相覆盖。
 */
export type ChannelTaskDraftAction =
  | {
      type: 'select-active'
      algoId: string | null
      currentRegionJson: string
    }
  | {
      type: 'toggle-algo'
      algoId: string
      checked: boolean
      configJson: string
      currentRegionJson: string
      nextActiveAlgoId: string | null
    }
  | {
      type: 'change-camera'
      cameraId: string
      drafts: Record<string, AlgoDraftItem>
      activeAlgoId: string | null
      currentRegionJson: string
      currentCrop: Rect | null
      nextCrop: Rect | null
      channelEnabled: boolean
    }
  | { type: 'update-fps'; algoId: string; sampleFps: number }
  | { type: 'update-config-json'; algoId: string; configJson: string }
  | { type: 'update-schedule-json'; algoId: string; scheduleJson: string }
  | { type: 'set-channel-enabled'; enabled: boolean }

/** 只在当前相机这一层做局部更新，其余相机的草稿原样保留。 */
function withCurrentCameraDrafts(
  state: ChannelTaskDraftState,
  mutate: (
    draftsForCamera: Record<string, AlgoDraftItem>
  ) => Record<string, AlgoDraftItem>
): ChannelTaskDraftState {
  const draftsForCamera = state.draftsByCamera[state.cameraId] ?? {}
  return {
    ...state,
    draftsByCamera: {
      ...state.draftsByCamera,
      [state.cameraId]: mutate(draftsForCamera),
    },
  }
}

/** 把编辑缓冲写回指定算法。永不用陈旧快照整体替换整张草稿表。 */
function commitRegion(
  draftsForCamera: Record<string, AlgoDraftItem>,
  algoId: string | null,
  regionJson: string
): Record<string, AlgoDraftItem> {
  if (!algoId || !draftsForCamera[algoId]) return draftsForCamera
  return {
    ...draftsForCamera,
    [algoId]: { ...draftsForCamera[algoId], regionConfigJson: regionJson },
  }
}

export function channelTaskDraftReducer(
  state: ChannelTaskDraftState,
  action: ChannelTaskDraftAction
): ChannelTaskDraftState {
  switch (action.type) {
    case 'select-active': {
      if (action.algoId === state.activeAlgoId) return state
      const next = withCurrentCameraDrafts(state, (draftsForCamera) => ({
        ...commitRegion(
          draftsForCamera,
          state.activeAlgoId,
          action.currentRegionJson
        ),
      }))
      return { ...next, activeAlgoId: action.algoId }
    }

    case 'toggle-algo': {
      const next = withCurrentCameraDrafts(state, (draftsForCamera) => {
        const existing = draftsForCamera[action.algoId]
        const toggled = {
          ...draftsForCamera,
          [action.algoId]: {
            ...(existing ?? createDraftItem({ selected: false })),
            selected: action.checked,
            configJson: action.configJson,
          },
        }
        return commitRegion(
          toggled,
          state.activeAlgoId,
          action.currentRegionJson
        )
      })
      return { ...next, activeAlgoId: action.nextActiveAlgoId }
    }

    case 'change-camera': {
      if (action.cameraId === state.cameraId) return state
      const draftsForCamera = state.draftsByCamera[state.cameraId] ?? {}
      const committed = commitRegion(
        draftsForCamera,
        state.activeAlgoId,
        action.currentRegionJson
      )
      return {
        cameraId: action.cameraId,
        enabledByCamera: {
          ...state.enabledByCamera,
          [action.cameraId]: action.channelEnabled,
        },
        cropByCamera: {
          ...state.cropByCamera,
          [state.cameraId]: action.currentCrop,
          [action.cameraId]: action.nextCrop,
        },
        draftsByCamera: {
          ...state.draftsByCamera,
          [state.cameraId]: committed,
          [action.cameraId]: action.drafts,
        },
        activeAlgoId: action.activeAlgoId,
      }
    }

    case 'update-fps':
      return withCurrentCameraDrafts(state, (draftsForCamera) => ({
        ...draftsForCamera,
        [action.algoId]: {
          ...(draftsForCamera[action.algoId] ?? createDraftItem()),
          sampleFps: action.sampleFps,
        },
      }))

    case 'update-config-json':
      return withCurrentCameraDrafts(state, (draftsForCamera) => ({
        ...draftsForCamera,
        [action.algoId]: {
          ...(draftsForCamera[action.algoId] ?? createDraftItem()),
          configJson: action.configJson,
        },
      }))

    case 'update-schedule-json':
      return withCurrentCameraDrafts(state, (draftsForCamera) => ({
        ...draftsForCamera,
        [action.algoId]: {
          ...(draftsForCamera[action.algoId] ?? createDraftItem()),
          scheduleJson: action.scheduleJson,
        },
      }))

    case 'set-channel-enabled':
      return {
        ...state,
        enabledByCamera: {
          ...state.enabledByCamera,
          [state.cameraId]: action.enabled,
        },
      }

    default:
      return state
  }
}
