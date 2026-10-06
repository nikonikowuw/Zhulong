import { useEffect, useRef, useState } from 'react';
import { isWebCodecsSupported, StreamDecoder } from '../core/decoder';
import { streamPool } from '../core/streamPool';
import type { ConnectionStatus, ParsedPacket, StreamTelemetry } from '../core/types';

export interface UseLiveStreamOptions {
  cameraId?: string | null;
  role?: 'main' | 'sub';
  canvasRef?: React.RefObject<HTMLCanvasElement | null>;
  enabled?: boolean;
}

export interface UseLiveStreamResult {
  status: ConnectionStatus;
  telemetry: StreamTelemetry;
  isSupported: boolean;
  hasFirstFrame: boolean;
  error: string | null;
}

export function useLiveStream({
  cameraId,
  role = 'main',
  canvasRef,
  enabled = true,
}: UseLiveStreamOptions): UseLiveStreamResult {
  const isSupported = isWebCodecsSupported();
  const activeKey = enabled && cameraId ? `${cameraId}:${role}` : '';
  const [currentKey, setCurrentKey] = useState(activeKey);

  const [status, setStatus] = useState<ConnectionStatus>(
    activeKey ? 'connecting' : 'disconnected',
  );
  const [hasFirstFrame, setHasFirstFrame] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (currentKey !== activeKey) {
    setCurrentKey(activeKey);
    setHasFirstFrame(false);
    setError(null);
    setStatus(activeKey ? 'connecting' : 'disconnected');
  }

  const [telemetry, setTelemetry] = useState<StreamTelemetry>({
    fps: 0,
    bitrateKbps: 0,
    latencyMs: 0,
    droppedFrames: 0,
    codec: 'unknown',
  });

  // 遥测计数与渲染调度
  const frameCountRef = useRef(0);
  const byteCountRef = useRef(0);
  const lastMetricsTimeRef = useRef(0);
  const lastResolutionRef = useRef<string | null>(null);
  const decoderRef = useRef<StreamDecoder | null>(null);

  useEffect(() => {
    if (!enabled || !cameraId) {
      return;
    }

    frameCountRef.current = 0;
    byteCountRef.current = 0;
    lastMetricsTimeRef.current = Date.now();
    lastResolutionRef.current = null;

    let pendingFrame: VideoFrame | null = null;
    let rafId: number | null = null;

    const renderLoop = () => {
      if (pendingFrame && canvasRef?.current) {
        const frame = pendingFrame;
        pendingFrame = null;
        const canvas = canvasRef.current;
        if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
          canvas.width = frame.displayWidth;
          canvas.height = frame.displayHeight;
        }
        const ctx = canvas.getContext('2d');
        if (ctx) {
          ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
        }
        frame.close();
      }
      rafId = null;
    };

    // 初始化解码器
    if (isSupported) {
      decoderRef.current = new StreamDecoder({
        onFrame: (frame: VideoFrame) => {
          frameCountRef.current++;
          setHasFirstFrame(true);

          // 仅在分辨率变更时触发 React 状态更新，避免 30/60fps 持续触发组件重渲染
          const resStr = `${frame.displayWidth}x${frame.displayHeight}`;
          if (lastResolutionRef.current !== resStr) {
            lastResolutionRef.current = resStr;
            setTelemetry((prev) => ({
              ...prev,
              resolution: resStr,
            }));
          }

          // 利用 requestAnimationFrame 节流绘制；若旧帧未被消耗则先行释放以防显存泄漏
          if (typeof requestAnimationFrame !== 'undefined') {
            if (pendingFrame) {
              pendingFrame.close();
            }
            pendingFrame = frame;
            if (rafId === null) {
              rafId = requestAnimationFrame(renderLoop);
            }
          } else {
            if (canvasRef?.current) {
              const canvas = canvasRef.current;
              if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
                canvas.width = frame.displayWidth;
                canvas.height = frame.displayHeight;
              }
              const ctx = canvas.getContext('2d');
              if (ctx) {
                ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
              }
            }
            frame.close();
          }
        },
        onError: (err) => {
          setError(err.message);
        },
      });
    }

    // 统计定时器 (每 1s 刷新一次 FPS / 码率)
    const metricsInterval = setInterval(() => {
      const now = Date.now();
      const elapsedSec = (now - lastMetricsTimeRef.current) / 1000;
      if (elapsedSec > 0) {
        const calculatedFps = Math.round(frameCountRef.current / elapsedSec);
        const calculatedKbps = Math.round((byteCountRef.current * 8) / elapsedSec / 1000);

        setTelemetry((prev) => ({
          ...prev,
          fps: calculatedFps,
          bitrateKbps: calculatedKbps,
        }));

        frameCountRef.current = 0;
        byteCountRef.current = 0;
        lastMetricsTimeRef.current = now;
      }
    }, 1000);

    const unsubscribe = streamPool.subscribe(
      cameraId,
      role,
      (packet: ParsedPacket) => {
        byteCountRef.current += packet.payload.length;
        setTelemetry((prev) => {
          if (prev.codec !== packet.codec) {
            return { ...prev, codec: packet.codec };
          }
          return prev;
        });

        if (decoderRef.current) {
          decoderRef.current.decode(packet);
        }
      },
      (newStatus: ConnectionStatus) => {
        setStatus(newStatus);
      },
    );

    return () => {
      clearInterval(metricsInterval);
      if (rafId !== null && typeof cancelAnimationFrame !== 'undefined') {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      if (pendingFrame) {
        pendingFrame.close();
        pendingFrame = null;
      }
      unsubscribe();
      if (decoderRef.current) {
        decoderRef.current.close();
        decoderRef.current = null;
      }
    };
  }, [cameraId, role, enabled, isSupported, canvasRef]);

  return {
    status,
    telemetry,
    isSupported,
    hasFirstFrame,
    error,
  };
}
