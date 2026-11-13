import { type Algorithm } from '@/features/algorithms/contract'
import {
  buildConfigFormSchema,
  getDefaultConfigJson,
  isZeroConfig,
  parseSchema,
} from './algo-config-schema'
import { type Task } from './schema'

export const EMPTY_CONFIG_JSON = '{}'
export const MIN_SAMPLE_FPS = 1
export const MAX_SAMPLE_FPS = 30

/** 单个算法的草稿项。 */
export interface AlgoDraftItem {
  selected: boolean
  sampleFps: number
  enabled: boolean
  configJson: string
  regionConfigJson: string
  scheduleJson: string
}

export function createDraftItem(
  overrides: Partial<AlgoDraftItem> = {}
): AlgoDraftItem {
  return {
    selected: true,
    sampleFps: 5,
    enabled: true,
    configJson: EMPTY_CONFIG_JSON,
    regionConfigJson: EMPTY_CONFIG_JSON,
    scheduleJson: EMPTY_CONFIG_JSON,
    ...overrides,
  }
}

/** 未配置（空串或 `{}`）时回落到算法包声明的默认值，已配置过的保留用户输入。 */
export function resolveDraftConfigJson(
  algo: Algorithm | undefined,
  existing?: string
): string {
  if (existing && existing.trim() !== EMPTY_CONFIG_JSON) return existing
  return getDefaultConfigJson(algo?.configSchemaJson)
}

/** 由已保存任务 + 算法清单推出某相机的初始草稿。纯函数，可在初始化与事件回调里复用。 */
export function computeInitialDrafts(
  targetCamId: string,
  allTasks: Task[],
  algorithms: Algorithm[]
): Record<string, AlgoDraftItem> {
  const cameraTask = allTasks.find((task) => task.cameraId === targetCamId)
  const savedByAlgorithm = new Map(
    (cameraTask?.algorithms ?? []).map((item) => [item.algorithmId, item])
  )

  const drafts: Record<string, AlgoDraftItem> = {}
  for (const algo of algorithms) {
    const saved = savedByAlgorithm.get(algo.id)
    drafts[algo.id] = saved
      ? {
          selected: true,
          sampleFps: saved.sampleFps,
          enabled: saved.enabled,
          configJson: resolveDraftConfigJson(algo, saved.configJson),
          regionConfigJson: saved.regionConfigJson || EMPTY_CONFIG_JSON,
          scheduleJson: saved.scheduleJson || EMPTY_CONFIG_JSON,
        }
      : {
          selected: false,
          sampleFps: 5,
          enabled: true,
          configJson: resolveDraftConfigJson(algo),
          regionConfigJson: EMPTY_CONFIG_JSON,
          scheduleJson: EMPTY_CONFIG_JSON,
        }
  }
  return drafts
}

/** 默认激活项：第一个已勾选的算法，没有则退回清单首项。 */
export function pickActiveAlgo(
  drafts: Record<string, AlgoDraftItem>,
  algorithms: Algorithm[]
): string | null {
  const selected = Object.entries(drafts).find(([, draft]) => draft.selected)
  return selected ? selected[0] : (algorithms[0]?.id ?? null)
}

/** 取消勾选当前激活项后，接替它的算法。 */
export function pickNextActiveAfterUncheck(
  drafts: Record<string, AlgoDraftItem>,
  excludedAlgoId: string
): string | null {
  const remaining = Object.entries(drafts).find(
    ([id, draft]) => id !== excludedAlgoId && draft.selected
  )
  return remaining ? remaining[0] : null
}

export type DraftValidationIssue =
  | { kind: 'invalid-json'; algoId: string; algoName: string }
  | {
      kind: 'schema-violation'
      algoId: string
      algoName: string
      fieldPath?: string
      message?: string
    }

/**
 * 校验已选算法的 JSON 语法与 `config.schema.json` 约束，
 * 在提交后端前拦截 400 并定位到首个出错算法。
 */
export function validateSelectedAlgoDrafts(
  drafts: Record<string, AlgoDraftItem>,
  algoMap: Map<string, Algorithm>
): DraftValidationIssue | null {
  for (const [algoId, draft] of Object.entries(drafts)) {
    if (!draft.selected) continue
    const rawJson = draft.configJson?.trim() || EMPTY_CONFIG_JSON
    let parsed: unknown
    try {
      parsed = JSON.parse(rawJson)
    } catch {
      return {
        kind: 'invalid-json',
        algoId,
        algoName: algoMap.get(algoId)?.name || algoId,
      }
    }

    const algo = algoMap.get(algoId)
    if (algo?.configSchemaJson) {
      const parsedSchema = parseSchema(algo.configSchemaJson)
      if (parsedSchema && !isZeroConfig(parsedSchema)) {
        const validator = buildConfigFormSchema(parsedSchema)
        const result = validator.safeParse(parsed)
        if (!result.success) {
          const firstIssue = result.error.issues[0]
          return {
            kind: 'schema-violation',
            algoId,
            algoName: algo.name || algoId,
            fieldPath: firstIssue?.path?.join('.') || undefined,
            message: firstIssue?.message || undefined,
          }
        }
      }
    }
  }
  return null
}
