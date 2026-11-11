import { z } from 'zod'

/**
 * 兼容后端 Go JSON 对 nil 切片可能序列化出的 null 与缺省字段，统一归一化为非空数组。
 */
const nullableArray = <S extends z.ZodType>(item: S) =>
  z
    .array(item)
    .nullish()
    .transform((v) => v ?? [])

/**
 * 人脸候选匹配项契约
 */
export const faceCandidateSchema = z.object({
  person_id: z.string().default(''),
  person_name: z.string().default(''),
  face_id: z.string().optional().default(''),
  similarity: z.number().default(0),
})

export type FaceCandidate = z.infer<typeof faceCandidateSchema>

/**
 * 挂载在人体下的人脸子目标契约
 * 坐标为严格归一化 [x1, y1, x2, y2] ∈ [0.0, 1.0]
 */
export const faceSubItemSchema = z.object({
  box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  quality: z.number().default(0),
  landmarks: nullableArray(z.tuple([z.number(), z.number()])),
  matched: z.boolean().default(false),
  person_id: z.string().default(''),
  person_name: z.string().default(''),
  matched_face_id: z.string().optional().default(''),
  similarity: z.number().default(0),
  is_fused: z.boolean().optional().default(false),
  candidates: nullableArray(faceCandidateSchema),
})

export type FaceSubItem = z.infer<typeof faceSubItemSchema>
export type FaceSubItemInput = z.input<typeof faceSubItemSchema>

/**
 * SCENE_STRUCTURE v2 顶层独立人脸项契约（person_index >= 0 关联人体，-1 为孤立人脸）
 */
export const faceStructureItemSchema = faceSubItemSchema.extend({
  person_index: z.number().int().default(-1),
})

export type FaceStructureItem = z.infer<typeof faceStructureItemSchema>

/**
 * 结构化人体项契约（兼容旧版嵌套 faces 与 v2 空切片）
 */
export const personItemSchema = z.object({
  track_id: z.number().default(0),
  body_box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  body_score: z.number().default(0),
  faces: nullableArray(faceSubItemSchema),
})

export type PersonItem = z.infer<typeof personItemSchema>
export type PersonItemInput = z.input<typeof personItemSchema>

/**
 * 挂载在机动车下的车牌子目标契约
 * 坐标为严格归一化 [x1, y1, x2, y2] ∈ [0.0, 1.0]
 */
export const plateSubItemSchema = z.object({
  box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  score: z.number().default(0),
  plate_number: z.string().default(''),
  plate_color: z.string().default(''),
  plate_type: z.number().default(0),
})

export type PlateSubItem = z.infer<typeof plateSubItemSchema>
export type PlateSubItemInput = z.input<typeof plateSubItemSchema>

/**
 * 结构化机动车项契约（1:N 挂载车牌）
 */
export const vehicleItemSchema = z.object({
  track_id: z.number().default(0),
  vehicle_box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  vehicle_score: z.number().default(0),
  vehicle_type: z.string().optional().default(''),
  vehicle_color: z.string().optional().default(''),
  plates: nullableArray(plateSubItemSchema),
})

export type VehicleItem = z.infer<typeof vehicleItemSchema>
export type VehicleItemInput = z.input<typeof vehicleItemSchema>

/**
 * 基础目标检测项契约
 */
export const detectionItemSchema = z.object({
  box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  label: z.string().default(''),
  score: z.number().default(0),
})

export type DetectionItem = z.infer<typeof detectionItemSchema>

/**
 * 统一感知推理结果事件契约（EventTypeAIResult / ai_result）
 */
export const aiResultEventSchema = z.object({
  camera_id: z.string(),
  algorithm_id: z.string().optional().default(''),
  pts: z.number().default(0),
  /** 与同源主流 WebCodecs VideoFrame.timestamp 对齐的归一化微秒时间戳（首帧合法值为 0） */
  pts_us: z.number().optional(),
  timestamp: z.string().optional(),
  task_type: z.string().default('person_structure'),
  cost_ms: z.number().default(0),
  detections: nullableArray(detectionItemSchema),
  classifications: nullableArray(z.unknown()),
  persons: nullableArray(personItemSchema),
  faces: nullableArray(faceStructureItemSchema),
  vehicles: nullableArray(vehicleItemSchema),
})

export type AIResultEvent = z.infer<typeof aiResultEventSchema>
export type AIResultEventInput = z.input<typeof aiResultEventSchema>

/**
 * 统一提取事件中的人脸列表（优先读取 SCENE_STRUCTURE v2 顶层 faces，回退兼容旧版嵌套 persons[].faces）
 */
export function extractFacesFromEvent(
  event: AIResultEvent
): FaceStructureItem[] {
  if (event.faces.length > 0) {
    return event.faces
  }
  const flattened: FaceStructureItem[] = []
  for (let i = 0; i < event.persons.length; i++) {
    const person = event.persons[i]
    for (const face of person.faces) {
      flattened.push({
        ...face,
        person_index: i,
      })
    }
  }
  return flattened
}

/**
 * 创建人脸项 Mock 辅助函数
 */
export function createMockFace(
  overrides: Partial<FaceSubItem> = {}
): FaceSubItem {
  return {
    box: [0.1, 0.1, 0.3, 0.3],
    quality: 0.9,
    landmarks: [],
    matched: false,
    person_id: '',
    person_name: '',
    matched_face_id: '',
    similarity: 0,
    is_fused: false,
    candidates: [],
    ...overrides,
  }
}

/**
 * 创建人体项 Mock 辅助函数
 */
export function createMockPerson(
  overrides: Partial<PersonItem> = {}
): PersonItem {
  return {
    track_id: 0,
    body_box: [0.05, 0.05, 0.6, 0.9],
    body_score: 0.9,
    faces: [],
    ...overrides,
  }
}

/**
 * 创建机动车项 Mock 辅助函数
 */
export function createMockVehicle(
  overrides: Partial<VehicleItem> = {}
): VehicleItem {
  return {
    track_id: 0,
    vehicle_box: [0.1, 0.1, 0.5, 0.5],
    vehicle_score: 0.9,
    vehicle_type: '',
    vehicle_color: '',
    plates: [],
    ...overrides,
  }
}

/**
 * 安全解析 AI 感知事件
 */
export function parseAIResultEvent(data: unknown): AIResultEvent | null {
  try {
    const raw = typeof data === 'string' ? JSON.parse(data) : data
    const res = aiResultEventSchema.safeParse(raw)
    if (res.success) {
      return res.data
    }
    return null
  } catch {
    return null
  }
}
