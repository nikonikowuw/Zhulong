import React from 'react';
import type { StreamTelemetry } from '../core/types';

interface LiveTelemetryHudProps {
  telemetry: StreamTelemetry;
  visible?: boolean;
}

export function LiveTelemetryHud({
  telemetry,
  visible = true,
}: LiveTelemetryHudProps): React.JSX.Element | null {
  if (!visible) return null;

  const codecLabel = telemetry.codec !== 'unknown' ? telemetry.codec : 'RAW';

  return (
    <div className="absolute bottom-2.5 right-2.5 z-10 flex items-center gap-2 px-2.5 py-1 rounded-full text-[11px] font-mono tracking-tight backdrop-blur-md bg-black/60 text-white/90 border border-white/10 shadow-sm pointer-events-none select-none">
      <span className="uppercase font-semibold text-emerald-400">
        {codecLabel}
      </span>
      {telemetry.resolution && (
        <>
          <span className="text-white/30">•</span>
          <span>{telemetry.resolution}</span>
        </>
      )}
      <span className="text-white/30">•</span>
      <span>{telemetry.fps} FPS</span>
      <span className="text-white/30">•</span>
      <span>{telemetry.bitrateKbps} kbps</span>
    </div>
  );
}
