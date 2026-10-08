export type CodecType = 'h264' | 'h265' | 'unknown';

export interface ParsedPacket {
  codec: CodecType;
  codecByte: number;
  flags: number;
  isKeyFrame: boolean;
  hasPts: boolean;
  hasDts: boolean;
  pts: bigint;
  dts: bigint;
  ptsMs: number;
  dtsMs: number;
  payload: Uint8Array;
}

export type ConnectionStatus =
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'disconnected'
  | 'error';

export interface StreamTelemetry {
  fps: number;
  bitrateKbps: number;
  latencyMs: number;
  droppedFrames: number;
  codec: CodecType;
  resolution?: string;
}

export type LayoutMode = 1 | 4 | 9;

export interface SlotBinding {
  cameraId: string;
  role: 'main' | 'sub';
  name?: string;
}

export interface LiveLayoutState {
  mode: LayoutMode;
  slots: Record<number, SlotBinding | null>;
  selectedSlotIndex?: number;
}

export interface RoiBox {
  id?: string;
  label?: string;
  score?: number;
  x: number; // 0..1 normalized
  y: number; // 0..1 normalized
  width: number; // 0..1 normalized
  height: number; // 0..1 normalized
  color?: string;
}

export interface RoiOverlayConfig {
  showLabels?: boolean;
  showScores?: boolean;
  boxColor?: string;
}
