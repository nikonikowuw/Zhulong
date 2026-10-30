import { z } from 'zod'
import { type TFunction } from 'i18next'

export const canvasMapSchema = z.object({
  id: z.string(),
  name: z.string(),
  imageUrl: z.string(),
  width: z.number(),
  height: z.number(),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type CanvasMap = z.infer<typeof canvasMapSchema>

export const cameraPositionInfoSchema = z.object({
  cameraId: z.string(),
  cameraName: z.string(),
  x: z.number().min(0).max(1),
  y: z.number().min(0).max(1),
  angle: z.number().min(0).max(360),
})

export type CameraPositionInfo = z.infer<typeof cameraPositionInfoSchema>

export const cameraPositionItemSchema = z.object({
  cameraId: z.string(),
  x: z.number().min(0).max(1),
  y: z.number().min(0).max(1),
  /** 朝向角，半开区间 [0, 360)。 */
  angle: z.number().min(0).lt(360),
})

export type CameraPositionItem = z.infer<typeof cameraPositionItemSchema>

export const saveCameraPositionsResponseSchema = z.object({
  mapId: z.string(),
  count: z.number(),
})

export type SaveCameraPositionsResponse = z.infer<
  typeof saveCameraPositionsResponseSchema
>

export const trajectoryNodeInfoSchema = z.object({
  seq: z.number(),
  cameraId: z.string(),
  cameraName: z.string(),
  x: z.number(),
  y: z.number(),
  firstSeenAt: z.string(),
  lastSeenAt: z.string(),
  dwellSec: z.number(),
  /** 媒体相对路径（如 snapshots/...），不是可直接加载的 URL。 */
  snapshotPath: z.string(),
  score: z.number(),
  details: z.string().optional(),
})

export type TrajectoryNodeInfo = z.infer<typeof trajectoryNodeInfoSchema>

export const trajectoryResponseSchema = z.object({
  mapId: z.string(),
  targetType: z.enum(['vehicle', 'person']),
  targetKey: z.string(),
  nodes: z.array(trajectoryNodeInfoSchema),
})

export type TrajectoryResponse = z.infer<typeof trajectoryResponseSchema>

export interface TrajectoryQueryParams {
  mapId: string
  targetType: 'vehicle' | 'person'
  plate?: string
  personId?: string
  startTime: string
  endTime: string
}

/**
 * 轨迹检索表单的校验 schema 工厂。
 *
 * 校验文案必须经 `t` 解析：zod 的 message 会原样进入 `FormMessage`，
 * 直接写 key 用户看到的就是 key 本身。
 */
export function createTrajectorySearchFormSchema(t?: TFunction) {
  const msg = {
    plateRequired: t ? t('validation.plateRequired') : '请输入车牌号',
    personRequired: t ? t('validation.personRequired') : '请选择底库人员',
    rangeRequired: t ? t('validation.rangeRequired') : '请选择时间范围',
  }

  return z
    .object({
      targetType: z.enum(['vehicle', 'person']),
      plate: z.string(),
      personId: z.string(),
      timeRange: z.enum(['1h', 'today', '24h', '7d']),
    })
    .superRefine((values, ctx) => {
      if (!values.timeRange) {
        ctx.addIssue({
          code: 'custom',
          path: ['timeRange'],
          message: msg.rangeRequired,
        })
        return
      }
      if (values.targetType === 'vehicle' && values.plate.trim() === '') {
        ctx.addIssue({
          code: 'custom',
          path: ['plate'],
          message: msg.plateRequired,
        })
      }
      if (values.targetType === 'person' && values.personId === '') {
        ctx.addIssue({
          code: 'custom',
          path: ['personId'],
          message: msg.personRequired,
        })
      }
    })
}

export type TrajectorySearchFormValues = z.infer<
  ReturnType<typeof createTrajectorySearchFormSchema>
>

export type TimeRangePreset = TrajectorySearchFormValues['timeRange']

/**
 * 底图名称字段：上传与重命名共用同一套长度与文案规则，避免两处漂移。
 */
function mapNameField(t?: TFunction) {
  return z
    .string()
    .min(1, t ? t('validation.mapNameRequired') : '请输入底图名称')
    .max(64, t ? t('validation.mapNameMax') : '底图名称不能超过 64 个字符')
}

/**
 * 底图上传表单的校验 schema 工厂。
 */
export function createMapUploadFormSchema(t?: TFunction) {
  return z.object({
    name: mapNameField(t),
    file: z.custom<File>(
      (value) => value instanceof File,
      t ? t('validation.mapFileRequired') : '请选择底图图片文件'
    ),
  })
}

export type MapUploadFormValues = z.infer<
  ReturnType<typeof createMapUploadFormSchema>
>

/**
 * 底图重命名表单的校验 schema 工厂。
 */
export function createMapRenameFormSchema(t?: TFunction) {
  return z.object({ name: mapNameField(t) })
}

export type MapRenameFormValues = z.infer<
  ReturnType<typeof createMapRenameFormSchema>
>
