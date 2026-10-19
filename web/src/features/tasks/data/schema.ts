import { z } from 'zod'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'

/**
 * 算法配置子项实体契约 (映射后端 TaskAlgorithm 实体)
 */
export const taskAlgorithmSchema = z.object({
  id: z.string(),
  taskId: z.string(),
  algorithmId: z.string(),
  enabled: z.boolean(),
  sampleFps: z.number().int().min(1).max(1000).default(5),
  configJson: z.string().default('{}'),
  regionConfigJson: z.string().default('{}'),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type TaskAlgorithm = z.infer<typeof taskAlgorithmSchema>

/**
 * 通道分析任务聚合根实体契约 (映射后端 Task 聚合根)
 */
export const taskSchema = z.object({
  id: z.string(),
  cameraId: z.string(),
  name: z.string().default(''),
  enabled: z.boolean(),
  algorithms: z.array(taskAlgorithmSchema).default([]),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Task = z.infer<typeof taskSchema>

/**
 * 分析任务列表响应契约
 */
export const taskListResponseSchema = z.array(taskSchema)
export type TaskListResponse = z.infer<typeof taskListResponseSchema>

/**
 * 单任务创建/更新表单契约
 */
export const taskFormSchema = z.object({
  cameraId: z.string().min(1, '请选择关联摄像头'),
  algorithmId: z.string().min(1, '请选择绑定算法包'),
  sampleFps: z
    .number()
    .int()
    .min(1, '采样帧率最小为 1')
    .max(30, '建议采样帧率不超过 30 FPS')
    .default(5),
  enabled: z.boolean().default(true),
  configJson: z
    .string()
    .refine(
      (val) => {
        if (!val || val.trim() === '') return true
        try {
          JSON.parse(val)
          return true
        } catch {
          return false
        }
      },
      { message: '配置参数必须是合法的 JSON 格式' }
    )
    .default('{}'),
})

export type TaskFormValues = z.infer<typeof taskFormSchema>

/**
 * 通道多算法配置草稿项
 */
export interface AlgorithmConfigDraft {
  algorithmId: string
  sampleFps: number
  enabled: boolean
  configJson: string
  regionConfigJson?: string
}

/**
 * 通道聚合下的单算法子项
 */
export interface ChannelTaskItem {
  taskId: string
  task: Task
  algorithmItem: TaskAlgorithm
  algorithm: Algorithm | undefined
  computePoints: number
}

/**
 * 通道分析任务聚合实体 (一个摄像头聚合关联多个算法任务)
 */
export interface ChannelTaskGroup {
  camera: Camera
  task?: Task
  items: ChannelTaskItem[]
  totalChannelPoints: number
  activeCount: number
  totalCount: number
}
