import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '@/shared/i18n';
import { LivePlayer, type LivePlayerHandle } from './LivePlayer';

describe('LivePlayer', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en');
  });

  afterEach(() => {
    cleanup();
  });

  it('renders canvas element and fallback or loading overlay', () => {
    const { container } = render(
      <LivePlayer cameraId="cam-1" role="main" showTelemetry={true} />,
    );

    const canvas = container.querySelector('canvas');
    expect(canvas).toBeInTheDocument();
  });

  it('renders WebCodecs unsupported warning when WebCodecs is absent in test environment', () => {
    render(<LivePlayer cameraId="cam-1" role="main" />);

    // In jsdom environment, window.VideoDecoder is undefined by default
    expect(screen.getByText('WebCodecs Unsupported')).toBeInTheDocument();
  });

  it('exposes takeSnapshot method via imperative handle', async () => {
    const playerRef = React.createRef<LivePlayerHandle>();
    render(
      <LivePlayer
        ref={playerRef}
        cameraId="cam-1"
        role="main"
      />,
    );

    expect(playerRef.current).toBeDefined();
    expect(typeof playerRef.current?.takeSnapshot).toBe('function');
    // Without first frame, takeSnapshot returns false
    const res = await playerRef.current?.takeSnapshot();
    expect(res).toBe(false);
  });
});
