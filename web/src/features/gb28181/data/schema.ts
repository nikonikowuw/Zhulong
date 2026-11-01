import { z } from 'zod'

/**
 * 后端字段约束的镜像。这些规则不是「前端偏好」：
 * 每一条都对应 `internal/gb28181/model.go` 的一个校验点，放宽会在保存时才炸。
 */

/** SIP 身份允许的字符集（后端 `validateIdentity`）。 */
export const SIP_IDENTITY_PATTERN = /^[A-Za-z0-9._-]+$/

/** GB28181 设备编号固定 20 位十进制数字（后端 `validateDeviceID`）。 */
const DEVICE_ID_PATTERN = /^\d{20}$/

export const IPV4_PATTERN =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

/** P0 唯一实现的安全档位；GB 35114 A/B/C 只保留接缝。 */
export const SUPPORTED_SECURITY_PROFILE = 'standard'

/** 端口成对租用 RTP/RTCP，容量即端口跨度的一半。 */
const MEDIA_PORT_PAIR_SIZE = 2

export const RTP_PORT_MIN = 1
export const RTP_PORT_MAX = 65535
/** 与后端 `validateDeviceUpsert` 同界，界面不要各写一份。 */
export const DEVICE_EXPIRES_MIN = 60
export const DEVICE_EXPIRES_MAX = 86400
export const SIP_IDENTITY_MAX_LENGTH = 64
/** 后端 `MediaBindAddress` / `MediaAdvertisedIP` 的 `binding:"max=64"`。 */
export const MEDIA_ADDRESS_MAX_LENGTH = 64
export const DEVICE_NAME_MAX_LENGTH = 128
export const DEVICE_PASSWORD_MAX_LENGTH = 128

export const SIP_TRANSPORTS = ['udp', 'tcp', 'both'] as const
export type SipTransport = (typeof SIP_TRANSPORTS)[number]

export const DEVICE_TRANSPORTS = ['udp', 'tcp'] as const
export type DeviceTransport = (typeof DEVICE_TRANSPORTS)[number]

/**
 * 解析 `host:port` 监听地址。返回 `null` 表示形态不合法，
 * 端口越界单独由调用方判断，以便给出精确的错误文案。
 */
export function parseSipListen(
  value: string
): { host: string; port: number } | null {
  const trimmed = value.trim()
  const separator = trimmed.lastIndexOf(':')
  if (separator <= 0) return null

  const host = trimmed.slice(0, separator)
  const portText = trimmed.slice(separator + 1)
  if (host === '' || !/^\d{1,5}$/.test(portText)) return null

  return { host, port: Number.parseInt(portText, 10) }
}

/** 端口区间能支撑的并发会话数；与后端 `mediaCapacity` 同算法。 */
export function mediaCapacity(
  minPort: number | undefined,
  maxPort: number | undefined
): number {
  if (minPort === undefined || maxPort === undefined) return 0
  const span = maxPort - minPort + 1
  if (span <= 0) return 0
  return Math.floor(span / MEDIA_PORT_PAIR_SIZE)
}

export function isValidDeviceID(value: string): boolean {
  return DEVICE_ID_PATTERN.test(value.trim())
}

// --- API 响应契约（不可信输入一律经 Zod 解析，不做类型断言） ---

export const gb28181ConfigSchema = z.object({
  enabled: z.boolean(),
  sipId: z.string(),
  sipRealm: z.string(),
  sipListen: z.string(),
  // 后端只接受 udp/tcp/both；未来新增传输方式时不应让整个页面解析失败，
  // 因此在这一层保持 string，由表单的 Select 负责可选项。
  sipTransport: z.string(),
  securityProfile: z.string(),
  mediaBindAddress: z.string(),
  mediaAdvertisedIp: z.string(),
  rtpPortMin: z.number(),
  rtpPortMax: z.number(),
  requireRegisteredDevice: z.boolean(),
})

export type Gb28181Config = z.infer<typeof gb28181ConfigSchema>

export const gb28181RuntimeInfoSchema = z.object({
  startupConfig: gb28181ConfigSchema,
  state: z.string(),
  detail: z.string(),
  advertisedIp: z.string(),
  restartRequired: z.boolean(),
  mediaCapacity: z.number(),
})

export type Gb28181RuntimeInfo = z.infer<typeof gb28181RuntimeInfoSchema>

export const gb28181ConfigResponseSchema = z.object({
  config: gb28181ConfigSchema,
  runtime: gb28181RuntimeInfoSchema,
})

export type Gb28181ConfigResponse = z.infer<typeof gb28181ConfigResponseSchema>

/**
 * 信令防护计数（对应 Go 的 `IngressCounters`）。
 *
 * 计数是精确的，只有日志行被采样：因此「上限是否在打人」以这里为准。
 */
export const gb28181IngressCountersSchema = z.object({
  oversizedMessages: z.number(),
  rejectedConns: z.number(),
  inboundLimited: z.number(),
  outboundLimited: z.number(),
  activeConns: z.number(),
})

export type Gb28181IngressCounters = z.infer<
  typeof gb28181IngressCountersSchema
>

export const gb28181RuntimeSnapshotStatusSchema = z.object({
  state: z.string(),
  restartRequired: z.boolean(),
  devices: z.object({
    total: z.number(),
    online: z.number(),
  }),
  sessions: z.object({
    active: z.number(),
  }),
  portPool: z.object({
    pairsTotal: z.number(),
    pairsUsed: z.number(),
    exhaustedHits: z.number(),
  }),
  ingress: gb28181IngressCountersSchema,
  generatedAt: z.string(),
})

export type Gb28181RuntimeSnapshotStatus = z.infer<
  typeof gb28181RuntimeSnapshotStatusSchema
>

export const gb28181DeviceSchema = z.object({
  deviceId: z.string(),
  name: z.string(),
  manufacturer: z.string().nullish(),
  model: z.string().nullish(),
  firmware: z.string().nullish(),
  transport: z.string(),
  remoteAddr: z.string().nullish(),
  expiresSeconds: z.number(),
  state: z.string(),
  channelCount: z.number().optional().default(0),
  lastKeepaliveAt: z.string().nullish(),
  registeredAt: z.string().nullish(),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Gb28181Device = z.infer<typeof gb28181DeviceSchema>

export const gb28181DeviceListSchema = z.object({
  items: z.array(gb28181DeviceSchema),
})

export type Gb28181DeviceList = z.infer<typeof gb28181DeviceListSchema>

export const gb28181ChannelSchema = z.object({
  deviceId: z.string(),
  channelId: z.string(),
  name: z.string(),
  manufacturer: z.string().nullish(),
  model: z.string().nullish(),
  owner: z.string().nullish(),
  civilCode: z.string().nullish(),
  address: z.string().nullish(),
  parental: z.number().default(0),
  parentId: z.string().nullish(),
  safetyWay: z.number().nullish(),
  registerWay: z.number().nullish(),
  ptzType: z.number().nullish(),
  resolutionWidth: z.number().nullish(),
  resolutionHeight: z.number().nullish(),
  cameraId: z.string().nullish(),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Gb28181Channel = z.infer<typeof gb28181ChannelSchema>

export const gb28181ChannelListSchema = z.object({
  items: z.array(gb28181ChannelSchema),
})

export type Gb28181ChannelList = z.infer<typeof gb28181ChannelListSchema>

export const syncCatalogResponseSchema = z.object({
  deviceId: z.string(),
  total: z.number(),
})

export type SyncCatalogResponse = z.infer<typeof syncCatalogResponseSchema>

/** `PUT /gb28181/devices/:deviceId` 的请求体。缺省字段保持已存值。 */
export interface UpsertGb28181DeviceRequest {
  name?: string
  password?: string
  transport?: DeviceTransport
  expiresSeconds?: number
}

// --- 配置字段清单与「待生效 / 运行值」对比 ---

export const GB28181_CONFIG_FIELDS = [
  'enabled',
  'sipId',
  'sipRealm',
  'sipListen',
  'sipTransport',
  'securityProfile',
  'mediaBindAddress',
  'mediaAdvertisedIp',
  'rtpPortMin',
  'rtpPortMax',
  'requireRegisteredDevice',
] as const

export type Gb28181ConfigField = (typeof GB28181_CONFIG_FIELDS)[number]

export type Gb28181ConfigValue = Gb28181Config[Gb28181ConfigField]

export interface Gb28181ConfigDifference {
  field: Gb28181ConfigField
  desired: Gb28181ConfigValue
  startup: Gb28181ConfigValue
}

/**
 * 列出「已保存配置」与「本进程启动配置」的差异。
 *
 * 这是重启提示的唯一依据：保存不会重配运行中的 SIP listener，
 * 因此不能把 desired 值直接当成 active 状态展示。
 */
export function diffGb28181Config(
  desired: Gb28181Config,
  startup: Gb28181Config
): Gb28181ConfigDifference[] {
  const differences: Gb28181ConfigDifference[] = []
  for (const field of GB28181_CONFIG_FIELDS) {
    if (desired[field] !== startup[field]) {
      differences.push({
        field,
        desired: desired[field],
        startup: startup[field],
      })
    }
  }
  return differences
}
