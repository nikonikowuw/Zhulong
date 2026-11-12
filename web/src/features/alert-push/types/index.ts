export type ChannelType = 'webhook' | 'mqtt'
export type ImageMode = 'url' | 'multipart' | 'base64'
export type AuthType = 'none' | 'bearer' | 'basic'

export interface WebhookConfig {
  url?: string
  heartbeatUrl?: string
  authType: AuthType
  timeoutSeconds?: number
  heartbeatIntervalSeconds?: number
}

export interface MQTTConfig {
  broker: string
  clientId?: string
  username?: string
  topic: string
  heartbeatTopic?: string
  lwtEnabled?: boolean
  qos?: number
  keepAliveSeconds?: number
  tlsSkipVerify?: boolean
  heartbeatIntervalSeconds?: number
}

export interface DestinationFilter {
  eventTypes?: string[]
  algorithmIds: string[]
  cameraIds: string[]
}

export interface DestinationSecret {
  token?: string
  username?: string
  password?: string
  headers?: Record<string, string>
}

export interface DestinationStatus {
  lastAttemptAt?: string
  lastSuccessAt?: string
  lastFailureAt?: string
  lastFailureReason?: string
  consecutiveFailures: number
  totalDispatched: number
  dropped?: number
  queueLength: number
  queueCapacity: number
  state: 'idle' | 'running' | 'degraded' | 'disabled'
}

export interface WebhookAlertDestination {
  id: string
  name: string
  channelType: 'webhook'
  enabled: boolean
  eventsEnabled: boolean
  heartbeatEnabled: boolean
  imageMode: ImageMode
  config: WebhookConfig
  filter: DestinationFilter
  secretSet: boolean
  headerNames?: string[]
  status: DestinationStatus
}

export interface MQTTAlertDestination {
  id: string
  name: string
  channelType: 'mqtt'
  enabled: boolean
  eventsEnabled: boolean
  heartbeatEnabled: boolean
  imageMode: ImageMode
  config: MQTTConfig
  filter: DestinationFilter
  secretSet: boolean
  headerNames?: string[]
  status: DestinationStatus
}

/** 辨识联合类型：以 channelType 作为判别式标签 */
export type AlertDestination = WebhookAlertDestination | MQTTAlertDestination

/** 通用或未分流时的配置投影类型 */
export type DestinationConfig = WebhookConfig & Partial<MQTTConfig>

export interface CreateAlertDestinationInput {
  name: string
  channelType: ChannelType
  eventsEnabled?: boolean
  heartbeatEnabled?: boolean
  imageMode?: ImageMode
  config: Record<string, unknown>
  filter?: DestinationFilter
  secret?: DestinationSecret
}

export interface UpdateAlertDestinationInput {
  name?: string
  eventsEnabled?: boolean
  heartbeatEnabled?: boolean
  imageMode?: ImageMode
  config?: Record<string, unknown>
  filter?: DestinationFilter
  secret?: DestinationSecret
}

export interface TestDestinationInput {
  id?: string
  channelType?: ChannelType
  eventsEnabled?: boolean
  heartbeatEnabled?: boolean
  imageMode?: ImageMode
  config?: Record<string, unknown>
  secret?: DestinationSecret
}

export interface TestDestinationResult {
  ok: boolean
  latencyMs: number
  reason?: string
}
