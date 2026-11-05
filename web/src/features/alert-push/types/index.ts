export type ChannelType = 'webhook' | 'mqtt'
export type ImageMode = 'url' | 'multipart' | 'base64'
export type AuthType = 'none' | 'bearer' | 'basic'

export interface WebhookConfig {
  url?: string
  authType: AuthType
  timeoutSeconds?: number
}

export interface MQTTConfig {
  broker: string
  clientId?: string
  username?: string
  topic: string
  qos?: number
  keepAliveSeconds?: number
  tlsSkipVerify?: boolean
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
  queueLength: number
  queueCapacity: number
  state: 'idle' | 'running' | 'degraded' | 'disabled'
}

export interface AlertDestination {
  id: string
  name: string
  channelType: ChannelType
  enabled: boolean
  imageMode: ImageMode
  config: WebhookConfig & MQTTConfig
  filter: DestinationFilter
  secretSet: boolean
  headerNames?: string[]
  status: DestinationStatus
}

export interface CreateAlertDestinationInput {
  name: string
  channelType: ChannelType
  imageMode: ImageMode
  config: Record<string, unknown>
  filter?: DestinationFilter
  secret?: DestinationSecret
}

export interface UpdateAlertDestinationInput {
  name?: string
  imageMode?: ImageMode
  config?: Record<string, unknown>
  filter?: DestinationFilter
  secret?: DestinationSecret
}

export interface TestDestinationInput {
  id?: string
  channelType?: ChannelType
  imageMode?: ImageMode
  config?: Record<string, unknown>
  secret?: DestinationSecret
}

export interface TestDestinationResult {
  ok: boolean
  latencyMs: number
  reason?: string
}
