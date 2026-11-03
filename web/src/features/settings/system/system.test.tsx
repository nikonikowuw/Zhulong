import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { NetworkTable } from './components/network-table'
import { StatCards } from './components/stat-cards'
import { StorageTable } from './components/storage-table'
import { ThermalList } from './components/thermal-list'
import {
  type CPUStatus,
  type MemoryStatus,
  type NPUStatus,
  type NetworkInterfaceStatus,
  type StorageMountStatus,
  type TemperatureInfo,
} from './types'
import { formatBytes, formatRate, formatUptime } from './utils/format'

describe('System Telemetry Utils', () => {
  it('formats bytes correctly', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1024 * 1024 * 512)).toBe('512 MB')
    expect(formatBytes(1024 * 1024 * 1024 * 4)).toBe('4 GB')
  })

  it('formats rates correctly', () => {
    expect(formatRate(0)).toBe('0 B/s')
    expect(formatRate(1024 * 1024)).toBe('1 MB/s')
  })

  it('formats uptime correctly', () => {
    expect(formatUptime(0)).toBe('0m')
    expect(formatUptime(120)).toBe('2m')
    expect(formatUptime(3660)).toBe('1h 1m')
    expect(formatUptime(90060)).toBe('1d 1h 1m')
  })
})

describe('StatCards Component', () => {
  const mockCPU: CPUStatus = {
    modelName: 'Cortex-A55',
    cores: 4,
    usagePercent: 32.5,
    coreUsages: [30, 35, 30, 35],
    temperature: 52.0,
  }

  const mockMemory: MemoryStatus = {
    totalBytes: 4 * 1024 * 1024 * 1024,
    usedBytes: 2 * 1024 * 1024 * 1024,
    availableBytes: 2 * 1024 * 1024 * 1024,
    freeBytes: 1024 * 1024 * 1024,
    buffersBytes: 512 * 1024 * 1024,
    cachedBytes: 512 * 1024 * 1024,
    usedPercent: 50.0,
    swapTotalBytes: 0,
    swapUsedBytes: 0,
    swapUsedPercent: 0,
  }

  const mockNPU: NPUStatus = {
    supported: true,
    name: 'Rockchip NPU',
    loadPercent: 18.0,
    coreLoads: [18.0],
    temperature: 50.0,
  }

  const mockNet: NetworkInterfaceStatus[] = [
    {
      interface: 'eth0',
      rxBytesRate: 1024 * 500,
      txBytesRate: 1024 * 200,
      rxTotalBytes: 10000000,
      txTotalBytes: 5000000,
      isUp: true,
    },
  ]

  const mockStorage: StorageMountStatus[] = [
    {
      mountPoint: '/',
      device: '/dev/mmcblk0p1',
      fsType: 'ext4',
      totalBytes: 32 * 1024 * 1024 * 1024,
      usedBytes: 14 * 1024 * 1024 * 1024,
      freeBytes: 18 * 1024 * 1024 * 1024,
      usedPercent: 43.8,
    },
  ]

  it('renders metric cards with correct values', async () => {
    const screen = await render(
      <StatCards
        cpu={mockCPU}
        memory={mockMemory}
        npu={mockNPU}
        network={mockNet}
        storage={mockStorage}
      />
    )

    await expect.element(screen.getByText('32.5%')).toBeVisible()
    await expect.element(screen.getByText('50.0%')).toBeVisible()
    await expect.element(screen.getByText('43.8%')).toBeVisible()
    await expect.element(screen.getByText('18.0%')).toBeVisible()
  })
})

describe('StorageTable & NetworkTable Components', () => {
  it('renders storage mounts list properly', async () => {
    const mockStorage: StorageMountStatus[] = [
      {
        mountPoint: '/data',
        device: '/dev/sda1',
        fsType: 'ext4',
        totalBytes: 100 * 1024 * 1024 * 1024,
        usedBytes: 40 * 1024 * 1024 * 1024,
        freeBytes: 60 * 1024 * 1024 * 1024,
        usedPercent: 40.0,
      },
    ]

    const screen = await render(<StorageTable storage={mockStorage} />)
    await expect.element(screen.getByText('/data')).toBeVisible()
    await expect.element(screen.getByText('/dev/sda1')).toBeVisible()
    await expect.element(screen.getByText('40.0%')).toBeVisible()
  })

  it('renders network interfaces properly', async () => {
    const mockNet: NetworkInterfaceStatus[] = [
      {
        interface: 'eth0',
        rxBytesRate: 1024 * 1024,
        txBytesRate: 512 * 1024,
        rxTotalBytes: 10000000,
        txTotalBytes: 5000000,
        isUp: true,
      },
    ]

    const screen = await render(<NetworkTable network={mockNet} />)
    await expect.element(screen.getByText('eth0')).toBeVisible()
  })

  it('renders thermal sensors list', async () => {
    const mockThermals: TemperatureInfo[] = [
      { label: 'CPU', temperature: 52.3 },
      { label: 'NPU', temperature: 49.5 },
    ]

    const screen = await render(<ThermalList thermals={mockThermals} />)
    await expect.element(screen.getByText('CPU')).toBeVisible()
    await expect.element(screen.getByText('52.3°C')).toBeVisible()
  })
})
