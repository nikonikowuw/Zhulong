import { apiClient, type ApiResponse } from '@/lib/api-client'
import { taskListResponseSchema, taskSchema, type Task } from '../data/schema'

export interface GetTasksParams {
  cameraId?: string
  algorithmId?: string
}

export interface TaskAlgorithmItemDto {
  id?: string
  algorithmId: string
  sampleFps?: number
  enabled?: boolean
  configJson?: string
  regionConfigJson?: string
}

export interface SaveChannelTaskDto {
  cameraId: string
  name?: string
  enabled?: boolean
  algorithms: TaskAlgorithmItemDto[]
}

export interface CreateTaskDto {
  cameraId: string
  algorithmId?: string
  sampleFps?: number
  enabled?: boolean
  configJson?: string
  regionConfigJson?: string
  name?: string
  algorithms?: TaskAlgorithmItemDto[]
}

export interface UpdateTaskDto {
  name?: string
  enabled?: boolean
  sampleFps?: number
  configJson?: string
  regionConfigJson?: string
  algorithms?: TaskAlgorithmItemDto[]
}

export const tasksApi = {
  /**
   * 获取分析任务列表，支持按摄像头与算法筛选
   */
  async getTasks(params?: GetTasksParams): Promise<Task[]> {
    const resp = await apiClient.get<ApiResponse<unknown>>('/tasks', {
      params: {
        camera_id: params?.cameraId,
        algorithm_id: params?.algorithmId,
      },
    })
    return taskListResponseSchema.parse(resp.data.data ?? [])
  },

  /**
   * 获取指定分析任务详情
   */
  async getTask(id: string): Promise<Task> {
    const resp = await apiClient.get<ApiResponse<unknown>>(`/tasks/${id}`)
    return taskSchema.parse(resp.data.data)
  },

  /**
   * 按摄像头获取通道分析任务详情
   */
  async getTaskByCamera(cameraId: string): Promise<Task> {
    const resp = await apiClient.get<ApiResponse<unknown>>(
      `/tasks/camera/${cameraId}`
    )
    return taskSchema.parse(resp.data.data)
  },

  /**
   * 保存通道多算法分析任务（原子 Upsert）
   */
  async saveChannelTask(dto: SaveChannelTaskDto): Promise<Task> {
    const resp = await apiClient.put<ApiResponse<unknown>>(
      `/tasks/camera/${dto.cameraId}`,
      dto
    )
    return taskSchema.parse(resp.data.data)
  },

  /**
   * 创建新的分析任务
   */
  async createTask(dto: CreateTaskDto): Promise<Task> {
    const resp = await apiClient.post<ApiResponse<unknown>>('/tasks', {
      cameraId: dto.cameraId,
      name: dto.name,
      enabled: dto.enabled,
      algorithmId: dto.algorithmId,
      sampleFps: dto.sampleFps,
      configJson: dto.configJson,
      regionConfigJson: dto.regionConfigJson,
      algorithms: dto.algorithms,
    })
    return taskSchema.parse(resp.data.data)
  },

  /**
   * 更新已有分析任务
   */
  async updateTask(id: string, dto: UpdateTaskDto): Promise<Task> {
    const resp = await apiClient.put<ApiResponse<unknown>>(`/tasks/${id}`, {
      name: dto.name,
      enabled: dto.enabled,
      sampleFps: dto.sampleFps,
      configJson: dto.configJson,
      regionConfigJson: dto.regionConfigJson,
      algorithms: dto.algorithms,
    })
    return taskSchema.parse(resp.data.data)
  },

  /**
   * 删除分析任务
   */
  async deleteTask(id: string): Promise<void> {
    await apiClient.delete(`/tasks/${id}`)
  },
}
