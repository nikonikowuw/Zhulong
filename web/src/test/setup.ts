import "@testing-library/jest-dom/vitest";

// Mock HTMLCanvasElement.prototype.getContext for jsdom environment
HTMLCanvasElement.prototype.getContext = (() => ({
  clearRect: () => {},
  fillRect: () => {},
  strokeRect: () => {},
  fillText: () => {},
  measureText: () => ({ width: 0 }),
  drawImage: () => {},
} as unknown as CanvasRenderingContext2D)) as unknown as typeof HTMLCanvasElement.prototype.getContext;

HTMLCanvasElement.prototype.toDataURL = (() => 'data:image/png;base64,mock') as unknown as typeof HTMLCanvasElement.prototype.toDataURL;
