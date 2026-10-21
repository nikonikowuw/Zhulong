import { z } from 'zod'

/**
 * 人脸候选匹配项契约
 */
export const faceCandidateSchema = z.object({
  person_id: z.string().default(''),
  person_name: z.string().default(''),
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
  landmarks: z
    .array(z.tuple([z.number(), z.number()]))
    .optional()
    .default([]),
  matched: z.boolean().default(false),
  person_id: z.string().default(''),
  person_name: z.string().default(''),
  similarity: z.number().default(0),
  is_fused: z.boolean().optional().default(false),
  candidates: z.array(faceCandidateSchema).optional().default([]),
})

export type FaceSubItem = z.infer<typeof faceSubItemSchema>
export type FaceSubItemInput = z.input<typeof faceSubItemSchema>

/**
 * 结构化人体项契约（1:N 挂载人脸）
 */
export const personItemSchema = z.object({
  track_id: z.number().default(0),
  body_box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  body_score: z.number().default(0),
  faces: z.array(faceSubItemSchema).default([]),
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
  plates: z.array(plateSubItemSchema).default([]),
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
  timestamp: z.string().optional(),
  task_type: z.string().default('person_structure'),
  cost_ms: z.number().default(0),
  detections: z.array(detectionItemSchema).optional().default([]),
  classifications: z.array(z.unknown()).optional().default([]),
  persons: z.array(personItemSchema).optional().default([]),
  vehicles: z.array(vehicleItemSchema).optional().default([]),
})

export type AIResultEvent = z.infer<typeof aiResultEventSchema>
export type AIResultEventInput = z.input<typeof aiResultEventSchema>

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
    similarity: 0,
    is_fused: false,
    candidates: [],
    ...overrides,
  }
}

/**
 * 实时抓拍卡片条目（用于实时识别记录流）
 */
export interface RecognitionRecordItem {
  id: string
  cameraId: string
  cameraName?: string
  personId: string
  personName: string
  matched: boolean
  similarity: number
  box: [number, number, number, number]
  timestamp: number
  pts: number
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
