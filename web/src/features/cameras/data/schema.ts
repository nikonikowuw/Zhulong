import { z } from 'zod'

const streamSchema = z.object({
  id: z.number(),
  role: z.enum(['main', 'sub']),
  protocol: z.string().default('rtsp'),
  rtspUrl: z.string(),
  transport: z.enum(['tcp', 'udp']).default('tcp'),
  codec: z.string().optional().default(''),
  width: z.number().optional().default(0),
  height: z.number().optional().default(0),
  fps: z.number().nullable().optional(),
  fpsString: z.string().optional().default(''),
  createdAt: z.string().optional(),
  updatedAt: z.string().optional(),
})

export type Stream = z.infer<typeof streamSchema>

export const cameraHealthStatusSchema = z.enum([
  'online',
  'offline',
  'error',
  'unknown',
])

export type CameraHealth = z.infer<typeof cameraHealthStatusSchema>

const cameraSessionStatusSchema = z.enum([
  'running',
  'idle',
  'starting',
  'error',
  'reconnecting',
])

export const cameraSchema = z.object({
  id: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  revision: z.number(),
  health: cameraHealthStatusSchema.default('unknown'),
  session: cameraSessionStatusSchema.default('idle'),
  degraded: z.boolean().default(false),
  stale: z.boolean().default(false),
  reason: z.string().optional().default(''),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  streams: z.array(streamSchema).default([]),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Camera = z.infer<typeof cameraSchema>

export const cameraListResponseSchema = z.object({
  items: z.array(cameraSchema),
  total: z.number(),
  page: z.number(),
  pageSize: z.number(),
})

export type CameraListResponse = z.infer<typeof cameraListResponseSchema>

export const cameraCredentialsResponseSchema = z.object({
  cameraId: z.string(),
  credentials: z.record(z.string(), z.string()), // role -> plaintext RTSP URL
})

export type CameraCredentialsResponse = z.infer<
  typeof cameraCredentialsResponseSchema
>

const streamStateInfoSchema = z.object({
  role: z.string(),
  health: z.string(),
  session: z.string(),
  evidenceType: z.string().optional(),
  reason: z.string().optional(),
  consecutiveFailures: z.number().optional(),
  errorMessage: z.string().optional(),
})

export const cameraStateInfoSchema = z.object({
  cameraId: z.string(),
  enabled: z.boolean(),
  revision: z.number(),
  health: z.string(),
  session: z.string(),
  degraded: z.boolean().default(false),
  stale: z.boolean().default(false),
  reason: z.string().optional(),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  streams: z.record(z.string(), streamStateInfoSchema).optional(),
})

export type CameraStateInfo = z.infer<typeof cameraStateInfoSchema>

export const diagnoseResponseSchema = z.object({
  cameraId: z.string(),
  state: cameraStateInfoSchema.optional(),
  message: z.string(),
})

export type DiagnoseResponse = z.infer<typeof diagnoseResponseSchema>

export const cameraFormSchema = z
  .object({
    name: z
      .string()
      .min(1, 'cameras.validation.nameRequired')
      .max(64, 'cameras.validation.nameMax'),
    enabled: z.boolean(),
    mainRtspUrl: z
      .string()
      .min(1, 'cameras.validation.mainRtspRequired')
      .regex(/^rtsp:\/\//i, 'cameras.validation.rtspInvalid'),
    mainTransport: z.enum(['tcp', 'udp']),
    hasSubStream: z.boolean(),
    subRtspUrl: z.string().optional(),
    subTransport: z.enum(['tcp', 'udp']),
  })
  .superRefine((data, ctx) => {
    if (data.hasSubStream) {
      if (!data.subRtspUrl || data.subRtspUrl.trim().length === 0) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['subRtspUrl'],
          message: 'cameras.validation.subRtspRequired',
        })
      } else if (!/^rtsp:\/\//i.test(data.subRtspUrl)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['subRtspUrl'],
          message: 'cameras.validation.rtspInvalid',
        })
      }
    }
  })

export type CameraFormValues = z.infer<typeof cameraFormSchema>
