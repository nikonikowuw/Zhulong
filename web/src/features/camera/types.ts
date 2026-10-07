import { z } from "zod";

export const healthStateSchema = z.enum(["online", "offline", "error", "unknown"]);
export type HealthState = z.infer<typeof healthStateSchema>;

export const sessionStateSchema = z.enum(["idle", "starting", "running", "reconnecting", "error"]);
export type SessionState = z.infer<typeof sessionStateSchema>;

export const streamRoleSchema = z.enum(["main", "sub"]);
export type StreamRole = z.infer<typeof streamRoleSchema>;

export const streamTransportSchema = z.enum(["tcp", "udp"]);
export type StreamTransport = z.infer<typeof streamTransportSchema>;

export const streamStateInfoSchema = z.object({
  role: z.string(),
  health: healthStateSchema,
  session: sessionStateSchema,
  evidenceType: z.string().optional(),
  reason: z.string().optional(),
  consecutiveFailures: z.number().optional(),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  errorMessage: z.string().optional(),
});
export type StreamStateInfo = z.infer<typeof streamStateInfoSchema>;

export const cameraStateInfoSchema = z.object({
  cameraId: z.string(),
  enabled: z.boolean(),
  revision: z.number(),
  health: healthStateSchema,
  session: sessionStateSchema,
  degraded: z.boolean().optional(),
  stale: z.boolean().optional(),
  reason: z.string().optional(),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  streams: z.record(z.string(), streamStateInfoSchema).optional(),
});
export type CameraStateInfo = z.infer<typeof cameraStateInfoSchema>;

export const streamResponseSchema = z.object({
  id: z.number(),
  role: streamRoleSchema,
  protocol: z.string(),
  rtspUrl: z.string(),
  transport: streamTransportSchema,
  codec: z.string(),
  width: z.number(),
  height: z.number(),
  fps: z.number().nullable().optional(),
  fpsString: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  runtimeState: streamStateInfoSchema.optional(),
});
export type StreamResponse = z.infer<typeof streamResponseSchema>;

export const cameraResponseSchema = z.object({
  id: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  revision: z.number(),
  health: healthStateSchema,
  session: sessionStateSchema,
  degraded: z.boolean().optional(),
  stale: z.boolean().optional(),
  reason: z.string().optional(),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  streams: z.array(streamResponseSchema),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type CameraResponse = z.infer<typeof cameraResponseSchema>;

export const cameraListSchema = z.preprocess(
  (val) => {
    if (val && typeof val === "object" && "items" in val && Array.isArray((val as { items: unknown }).items)) {
      return (val as { items: unknown }).items;
    }
    return val;
  },
  z.array(cameraResponseSchema),
);

export const cameraCredentialsResponseSchema = z.object({
  cameraId: z.string(),
  credentials: z.record(z.string(), z.string()),
});
export type CameraCredentialsResponse = z.infer<typeof cameraCredentialsResponseSchema>;

export const diagnoseResponseSchema = z.object({
  cameraId: z.string(),
  state: cameraStateInfoSchema.nullable().optional(),
  message: z.string(),
});
export type DiagnoseResponse = z.infer<typeof diagnoseResponseSchema>;

export interface CreateStreamInput {
  role: StreamRole;
  protocol?: string;
  rtspUrl: string;
  transport?: StreamTransport;
}

export interface CreateCameraInput {
  id?: string;
  name: string;
  enabled?: boolean;
  mainStream: CreateStreamInput;
  subStream?: CreateStreamInput;
}

export interface UpdateCameraInput {
  revision: number;
  name?: string;
  enabled?: boolean;
  mainStream?: CreateStreamInput;
  subStream?: CreateStreamInput;
}
