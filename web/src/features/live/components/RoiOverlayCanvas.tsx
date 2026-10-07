import React, { useEffect, useRef } from 'react';
import type { RoiBox, RoiOverlayConfig } from '../core/types';

interface RoiOverlayCanvasProps {
  boxes?: RoiBox[];
  config?: RoiOverlayConfig;
  className?: string;
}

export function RoiOverlayCanvas({
  boxes = [],
  config = {},
  className = '',
}: RoiOverlayCanvasProps): React.JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    ctx.clearRect(0, 0, canvas.width, canvas.height);
    if (boxes.length === 0) return;

    const width = canvas.width;
    const height = canvas.height;
    const showLabels = config.showLabels ?? true;
    const showScores = config.showScores ?? true;

    for (const box of boxes) {
      const x = box.x * width;
      const y = box.y * height;
      const w = box.width * width;
      const h = box.height * height;
      const strokeColor = box.color || config.boxColor || '#0a84ff';

      ctx.strokeStyle = strokeColor;
      ctx.lineWidth = 2;
      ctx.strokeRect(x, y, w, h);

      const labelParts: string[] = [];
      if (showLabels && box.label) labelParts.push(box.label);
      if (showScores && box.score !== undefined) labelParts.push(`${Math.round(box.score * 100)}%`);

      const labelText = labelParts.join(' ');
      if (labelText) {
        ctx.font = '11px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';
        const textMetrics = ctx.measureText(labelText);
        const textWidth = textMetrics.width;
        const textHeight = 14;

        ctx.fillStyle = strokeColor;
        ctx.fillRect(x, Math.max(0, y - textHeight - 4), textWidth + 8, textHeight + 4);

        ctx.fillStyle = '#ffffff';
        ctx.fillText(labelText, x + 4, Math.max(11, y - 4));
      }
    }
  }, [boxes, config]);

  return (
    <canvas
      ref={canvasRef}
      className={`absolute inset-0 w-full h-full pointer-events-none z-10 ${className}`}
      width={1920}
      height={1080}
    />
  );
}
