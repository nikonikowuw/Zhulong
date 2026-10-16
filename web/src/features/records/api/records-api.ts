import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import {
  type Alarm,
  type AlarmListResponse,
  alarmListResponseSchema,
  alarmSchema,
  type Capture,
  type CaptureListResponse,
  captureListResponseSchema,
  captureSchema,
  type GetAlarmsParams,
  type GetCapturesParams,
  type GetRecognitionsParams,
  type Recognition,
  type RecognitionListResponse,
  recognitionListResponseSchema,
  recognitionSchema,
} from '../data/schema'

export const recordsApi = {
  /**
   * 获取识别记录列表
   */
  async getRecognitions(
    params?: GetRecognitionsParams
  ): Promise<RecognitionListResponse> {
    const resp = await apiClient.get<unknown>('/records/recognitions', {
      params: {
        page: params?.page ?? 1,
        pageSize: params?.pageSize ?? 20,
        cameraId: params?.cameraId?.trim() || undefined,
        personId: params?.personId?.trim() || undefined,
        matched:
          params?.matched !== undefined ? String(params.matched) : undefined,
        startTime: params?.startTime?.trim() || undefined,
        endTime: params?.endTime?.trim() || undefined,
      },
    })

    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: recognitionListResponseSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取单条识别记录详情
   */
  async getRecognition(id: string): Promise<Recognition> {
    const resp = await apiClient.get<unknown>(
      `/records/recognitions/${encodeURIComponent(id)}`
    )
    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: recognitionSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取告警记录列表
   */
  async getAlarms(params?: GetAlarmsParams): Promise<AlarmListResponse> {
    const resp = await apiClient.get<unknown>('/records/alarms', {
      params: {
        page: params?.page ?? 1,
        pageSize: params?.pageSize ?? 20,
        cameraId: params?.cameraId?.trim() || undefined,
        alarmType: params?.alarmType?.trim() || undefined,
        startTime: params?.startTime?.trim() || undefined,
        endTime: params?.endTime?.trim() || undefined,
      },
    })

    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: alarmListResponseSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取单条告警记录详情
   */
  async getAlarm(id: string): Promise<Alarm> {
    const resp = await apiClient.get<unknown>(
      `/records/alarms/${encodeURIComponent(id)}`
    )
    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: alarmSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取抓拍记录列表
   */
  async getCaptures(params?: GetCapturesParams): Promise<CaptureListResponse> {
    const resp = await apiClient.get<unknown>('/records/captures', {
      params: {
        page: params?.page ?? 1,
        pageSize: params?.pageSize ?? 20,
        cameraId: params?.cameraId?.trim() || undefined,
        targetType: params?.targetType?.trim() || undefined,
        startTime: params?.startTime?.trim() || undefined,
        endTime: params?.endTime?.trim() || undefined,
      },
    })

    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: captureListResponseSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取单条抓拍记录详情
   */
  async getCapture(id: string): Promise<Capture> {
    const resp = await apiClient.get<unknown>(
      `/records/captures/${encodeURIComponent(id)}`
    )
    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: captureSchema,
      })
      .parse(resp.data).data
  },

  /**
   * 获取系统中已记录的全部唯一告警类型
   */
  async getAlarmTypes(): Promise<string[]> {
    const resp = await apiClient.get<unknown>('/records/alarms/alarm-types')
    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: z.array(z.string()),
      })
      .parse(resp.data).data
  },

  /**
   * 获取系统中已记录的全部唯一抓拍目标类型
   */
  async getCaptureTargetTypes(): Promise<string[]> {
    const resp = await apiClient.get<unknown>('/records/captures/target-types')
    return z
      .object({
        code: z.literal('OK'),
        message: z.string(),
        data: z.array(z.string()),
      })
      .parse(resp.data).data
  },
}
