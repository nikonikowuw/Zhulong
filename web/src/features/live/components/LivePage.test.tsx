import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/shared/i18n';
import type { CameraResponse } from '../../camera/types';
import { LivePage } from './LivePage';

const mockCameras: CameraResponse[] = [
  {
    id: 'cam-gate',
    name: 'Front Gate Camera',
    enabled: true,
    revision: 1,
    health: 'online',
    session: 'running',
    streams: [
      {
        id: 1,
        role: 'main',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://gate/main',
        transport: 'tcp',
        codec: 'h264',
        width: 1920,
        height: 1080,
        fpsString: '30',
        createdAt: '',
        updatedAt: '',
      },
    ],
    createdAt: '',
    updatedAt: '',
  },
];

const mockUseCamerasQuery = vi.fn();

vi.mock('../../camera/hooks/useCameras', () => ({
  useCamerasQuery: () => mockUseCamerasQuery(),
}));

function renderLivePage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <LivePage />
    </QueryClientProvider>,
  );
}

describe('LivePage', () => {
  beforeEach(async () => {
    localStorage.clear();
    await i18n.changeLanguage('zh-Hans');
    mockUseCamerasQuery.mockReturnValue({
      data: mockCameras,
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders both device tree panel and split-screen dashboard', () => {
    renderLivePage();

    // Left panel: Device tree
    expect(screen.getByText('媒体设备树')).toBeInTheDocument();
    expect(screen.getByText('Front Gate Camera')).toBeInTheDocument();

    // Right panel: Dashboard
    expect(screen.getByText('实时视频监控')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '1' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '4' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '9' })).toBeInTheDocument();
  });

  it('clicking a stream in device tree assigns it to the focused slot', () => {
    renderLivePage();

    // Single click the stream
    const streamRow = screen.getByText('主码流').closest('div');
    expect(streamRow).toBeTruthy();
    fireEvent.click(streamRow!);

    // Slot 1 and device tree now display the camera
    const cameraInstances = screen.getAllByText('Front Gate Camera');
    expect(cameraInstances.length).toBeGreaterThanOrEqual(2);

    // Device tree now shows playing badge for slot 1
    expect(screen.getByText('视口 1')).toBeInTheDocument();
  });

  it('supports dragging a stream and dropping it onto a specific slot', () => {
    renderLivePage();

    // Drop onto slot 3 (index 2)
    const slot3Element = screen.getByText('视口 3').closest('.aspect-video');
    expect(slot3Element).toBeTruthy();

    const dataPayload = JSON.stringify({
      cameraId: 'cam-gate',
      role: 'main',
      name: 'Front Gate Camera',
    });

    fireEvent.dragOver(slot3Element!, {
      dataTransfer: { dropEffect: 'none' },
    });

    fireEvent.drop(slot3Element!, {
      dataTransfer: {
        getData: (format: string) =>
          format === 'application/json' ? dataPayload : '',
      },
    });

    // Slot 3 should now play the camera
    expect(screen.getByText('视口 3')).toBeInTheDocument();
  });
});
