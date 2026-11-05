import { z } from 'zod'
import { apiClient } from '@/lib/api-client'
import type {
  AlertDestination,
  CreateAlertDestinationInput,
  TestDestinationInput,
  TestDestinationResult,
  UpdateAlertDestinationInput,
} from '../types'

export const destinationStatusSchema = z.object({
  lastAttemptAt: z.string().optional(),
  lastSuccessAt: z.string().optional(),
  lastFailureAt: z.string().optional(),
  lastFailureReason: z.string().optional(),
  consecutiveFailures: z.number().default(0),
  totalDispatched: z.number().default(0),
  queueLength: z.number().default(0),
  queueCapacity: z.number().default(32),
  state: z.enum(['idle', 'running', 'degraded', 'disabled']).default('idle'),
})

export const destinationFilterSchema = z.object({
  eventTypes: z.array(z.string()).default([]),
  algorithmIds: z.array(z.string()).default([]),
  cameraIds: z.array(z.string()).default([]),
})

export const webhookConfigSchema = z.object({
  url: z.string().optional(),
  authType: z.enum(['none', 'bearer', 'basic']).default('none'),
  timeoutSeconds: z.number().optional(),
})

export const mqttConfigSchema = z.object({
  broker: z.string().default(''),
  clientId: z.string().optional(),
  username: z.string().optional(),
  topic: z.string().default(''),
  qos: z.number().optional(),
  keepAliveSeconds: z.number().optional(),
  tlsSkipVerify: z.boolean().default(false),
})

const baseDestinationSchema = {
  id: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  imageMode: z.enum(['url', 'multipart', 'base64']),
  filter: destinationFilterSchema,
  secretSet: z.boolean().default(false),
  headerNames: z.array(z.string()).optional(),
  status: destinationStatusSchema,
}

export const webhookDestinationSchema = z.object({
  ...baseDestinationSchema,
  channelType: z.literal('webhook'),
  config: webhookConfigSchema,
})

export const mqttDestinationSchema = z.object({
  ...baseDestinationSchema,
  channelType: z.literal('mqtt'),
  config: mqttConfigSchema,
})

export const alertDestinationSchema = z.discriminatedUnion('channelType', [
  webhookDestinationSchema,
  mqttDestinationSchema,
])

export const alertDestinationListResponseSchema = z.object({
  items: z.array(alertDestinationSchema),
})

export const testResponseSchema = z.object({
  ok: z.boolean(),
  latencyMs: z.number(),
  reason: z.string().optional(),
})

export const alertPushApi = {
  async list(): Promise<AlertDestination[]> {
    const resp = await apiClient.get<unknown>('/alert-destinations')
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: alertDestinationListResponseSchema,
      })
      .parse(resp.data)
    return parsed.data.items
  },

  async create(input: CreateAlertDestinationInput): Promise<AlertDestination> {
    const resp = await apiClient.post<unknown>('/alert-destinations', input)
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: alertDestinationSchema,
      })
      .parse(resp.data)
    return parsed.data
  },

  async update(
    id: string,
    input: UpdateAlertDestinationInput
  ): Promise<AlertDestination> {
    const resp = await apiClient.put<unknown>(
      `/alert-destinations/${id}`,
      input
    )
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: alertDestinationSchema,
      })
      .parse(resp.data)
    return parsed.data
  },

  async delete(id: string): Promise<void> {
    await apiClient.delete<unknown>(`/alert-destinations/${id}`)
  },

  async enable(id: string): Promise<AlertDestination> {
    const resp = await apiClient.post<unknown>(
      `/alert-destinations/${id}/enable`
    )
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: alertDestinationSchema,
      })
      .parse(resp.data)
    return parsed.data
  },

  async disable(id: string): Promise<AlertDestination> {
    const resp = await apiClient.post<unknown>(
      `/alert-destinations/${id}/disable`
    )
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: alertDestinationSchema,
      })
      .parse(resp.data)
    return parsed.data
  },

  async test(input: TestDestinationInput): Promise<TestDestinationResult> {
    const resp = await apiClient.post<unknown>(
      '/alert-destinations/test',
      input
    )
    const parsed = z
      .object({
        code: z.literal('OK'),
        data: testResponseSchema,
      })
      .parse(resp.data)
    return parsed.data
  },
}
