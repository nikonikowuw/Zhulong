import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '@/shared/i18n';
import { LivePlayer } from './LivePlayer';

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
});
