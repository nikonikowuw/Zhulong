import i18n from '@/shared/i18n';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CameraResponse } from '../../../camera/types';
import { LiveDeviceTreePanel } from './LiveDeviceTreePanel';

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
        id: 101,
        role: 'main',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://test/main',
        transport: 'tcp',
        codec: 'h264',
        width: 1920,
        height: 1080,
        fpsString: '30',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 102,
        role: 'sub',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://test/sub',
        transport: 'tcp',
        codec: 'h264',
        width: 640,
        height: 360,
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

vi.mock('../../../camera/hooks/useCameras', () => ({
  useCamerasQuery: () => mockUseCamerasQuery(),
}));

describe('LiveDeviceTreePanel', () => {
  beforeEach(async () => {
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

  it('renders camera list and stream channels', () => {
    render(
      <LiveDeviceTreePanel
        getPlayingSlot={() => null}
        onPlay={vi.fn()}
      />,
    );

    expect(screen.getByText('Front Gate Camera')).toBeInTheDocument();
    expect(screen.getByText('主码流')).toBeInTheDocument();
    expect(screen.getByText('子码流')).toBeInTheDocument();
    expect(screen.getByText('1/1')).toBeInTheDocument();
  });

  it('filters cameras when search query is typed', () => {
    render(
      <LiveDeviceTreePanel
        getPlayingSlot={() => null}
        onPlay={vi.fn()}
      />,
    );

    const input = screen.getByPlaceholderText('搜索设备或通道...');
    fireEvent.change(input, { target: { value: 'NotFoundDevice' } });

    expect(screen.queryByText('Front Gate Camera')).not.toBeInTheDocument();
    expect(screen.getByText('未找到匹配的设备或通道')).toBeInTheDocument();
  });

  it('triggers onPlay when stream is clicked', () => {
    const handlePlay = vi.fn();
    render(
      <LiveDeviceTreePanel
        getPlayingSlot={() => null}
        onPlay={handlePlay}
      />,
    );

    const streamRow = screen.getByText('主码流').closest('div');
    expect(streamRow).toBeTruthy();
    fireEvent.click(streamRow!);

    expect(handlePlay).toHaveBeenCalledWith({
      cameraId: 'cam-gate',
      role: 'main',
      name: 'Front Gate Camera',
    });
  });

  it('sets dataTransfer on drag start of stream node', () => {
    render(
      <LiveDeviceTreePanel
        getPlayingSlot={() => null}
        onPlay={vi.fn()}
      />,
    );

    const streamRow = screen.getByText('主码流').closest('div');
    expect(streamRow).toBeTruthy();

    const setData = vi.fn();
    fireEvent.dragStart(streamRow!, {
      dataTransfer: {
        setData,
        effectAllowed: '',
      },
    });

    expect(setData).toHaveBeenCalledWith(
      'application/json',
      JSON.stringify({
        cameraId: 'cam-gate',
        role: 'main',
        name: 'Front Gate Camera',
      }),
    );
  });
});
