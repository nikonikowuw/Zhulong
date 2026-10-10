export type SyncState =
  | 'unsynced'
  | 'syncing'
  | 'synchronized'
  | 'panic_review'
  | 'failed'

export type RtcStatus = 'normal' | 'missing' | 'error'

export interface SyncStatus {
  state: SyncState
  lastSyncTime: string | null
  lastSyncServer: string
  offsetMs: number
  rttMs: number
  errorMessage: string
}

export interface SystemTimeStatus {
  currentTime: string
  timezone: string
  mode: 'ntp' | 'manual'
  ntpServers: string[]
  syncIntervalSeconds: number
  syncStatus: SyncStatus
  rtcStatus: RtcStatus
  hasPermission: boolean
}

export interface UpdateConfigPayload {
  mode: 'ntp' | 'manual'
  ntpServers: string[]
  syncIntervalSeconds: number
  timezone: string
}

export interface ManualTimePayload {
  targetTime: string
}
