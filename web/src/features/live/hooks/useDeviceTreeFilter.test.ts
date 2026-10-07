import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { CameraResponse } from '../../camera/types';
import { useDeviceTreeFilter } from './useDeviceTreeFilter';

const mockCameras: CameraResponse[] = [
  {
    id: 'cam-gate',
    name: 'Front Gate PTZ',
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
        width: 3840,
        height: 2160,
        fpsString: '30',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 2,
        role: 'sub',
        protocol: 'rtsp',
        rtspUrl: 'rtsp://gate/sub',
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
  {
    id: 'cam-park',
    name: 'Parking Lot East',
    enabled: true,
    revision: 1,
    health: 'offline',
    session: 'idle',
    streams: [],
    createdAt: '',
    updatedAt: '',
  },
];

describe('useDeviceTreeFilter', () => {
  it('returns all cameras and accurate stats initially', () => {
    const { result } = renderHook(() =>
      useDeviceTreeFilter({ cameras: mockCameras, defaultExpanded: true }),
    );

    expect(result.current.filteredCameras.length).toBe(2);
    expect(result.current.stats).toEqual({
      total: 2,
      online: 1,
      offline: 1,
      degraded: 0,
    });
    expect(result.current.isExpanded('cam-gate')).toBe(true);
  });

  it('filters cameras by name or id', () => {
    const { result } = renderHook(() =>
      useDeviceTreeFilter({ cameras: mockCameras }),
    );

    act(() => {
      result.current.setSearchQuery('Gate');
    });

    expect(result.current.filteredCameras.length).toBe(1);
    expect(result.current.filteredCameras[0]?.id).toBe('cam-gate');
    // When searching, matched item is automatically expanded
    expect(result.current.isExpanded('cam-gate')).toBe(true);
  });

  it('filters cameras by stream codec', () => {
    const { result } = renderHook(() =>
      useDeviceTreeFilter({ cameras: mockCameras }),
    );

    act(() => {
      result.current.setSearchQuery('h264');
    });

    expect(result.current.filteredCameras.length).toBe(1);
    expect(result.current.filteredCameras[0]?.id).toBe('cam-gate');
  });

  it('supports toggling expand and collapse all', () => {
    const { result } = renderHook(() =>
      useDeviceTreeFilter({ cameras: mockCameras, defaultExpanded: true }),
    );

    act(() => {
      result.current.toggleExpand('cam-gate');
    });
    expect(result.current.isExpanded('cam-gate')).toBe(false);

    act(() => {
      result.current.expandAll();
    });
    expect(result.current.isExpanded('cam-gate')).toBe(true);

    act(() => {
      result.current.collapseAll();
    });
    expect(result.current.isExpanded('cam-gate')).toBe(false);
  });
});
