import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { NetworkTable } from './components/network-table'
import { StatCards } from './components/stat-cards'
import { StorageTable } from './components/storage-table'
import { SystemHeader } from './components/system-header'
import { ThermalList } from './components/thermal-list'
import { TrendCharts } from './components/trend-charts'
import {
  type CPUStatus,
  type HardwareHistoryPoint,
  type HostInfo,
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

  it('renders idle message when NPU is supported but has 0% load', async () => {
    const idleNPU: NPUStatus = {
      ...mockNPU,
      loadPercent: 0,
      coreLoads: [0],
    }

    const screen = await render(
      <StatCards
        cpu={mockCPU}
        memory={mockMemory}
        npu={idleNPU}
        network={mockNet}
        storage={mockStorage}
      />
    )

    await expect
      .element(screen.getByText(/当前无加速模型负载|Idle/))
      .toBeVisible()
  })

  it('renders unsupported message when NPU is not supported', async () => {
    const unsupportedNPU: NPUStatus = {
      supported: false,
      name: 'None',
      loadPercent: 0,
      coreLoads: [],
      temperature: 0,
    }

    const screen = await render(
      <StatCards
        cpu={mockCPU}
        memory={mockMemory}
        npu={unsupportedNPU}
        network={mockNet}
        storage={mockStorage}
      />
    )

    await expect.element(screen.getByText('N/A')).toBeVisible()
    await expect
      .element(screen.getByText(/未检测到 NPU 加速硬件|No NPU/))
      .toBeVisible()
  })
})

describe('SystemHeader Component', () => {
  const mockHost: HostInfo = {
    hostname: 'edge-gateway-01',
    os: 'linux',
    arch: 'aarch64',
    kernelVersion: '5.10.160-rockchip',
    uptimeSeconds: 7200,
    bootTime: new Date().toISOString(),
    loadAvg: [0.35, 0.42, 0.5],
  }

  it('renders host info and handles live pause toggling', async () => {
    const togglePause = vi.fn()
    const refresh = vi.fn()

    const screen = await render(
      <SystemHeader
        host={mockHost}
        isRefetching={false}
        isPaused={false}
        onTogglePause={togglePause}
        onRefresh={refresh}
      />
    )

    await expect.element(screen.getByText('edge-gateway-01')).toBeVisible()
    await expect.element(screen.getByText(/自动刷新中|Live/)).toBeVisible()

    const pauseBtn = screen.getByRole('button', {
      name: /暂停自动刷新|Pause live telemetry/i,
    })
    await pauseBtn.click()
    expect(togglePause).toHaveBeenCalledTimes(1)
  })

  it('renders paused state when isPaused is true', async () => {
    const screen = await render(
      <SystemHeader
        host={mockHost}
        isRefetching={false}
        isPaused={true}
        onTogglePause={vi.fn()}
        onRefresh={vi.fn()}
      />
    )

    await expect.element(screen.getByText(/已暂停刷新|Paused/)).toBeVisible()
  })
})

describe('TrendCharts Component', () => {
  it('renders empty history guidance when history is empty', async () => {
    const screen = await render(
      <TrendCharts history={[]} npuSupported={false} />
    )

    await expect
      .element(
        screen.getByText(
          /历史时序数据采集积累中|Accumulating telemetry history/
        )
      )
      .toBeVisible()
  })

  /**
   * 回归「刻度被挤出图表容器」。
   *
   * 病根有两层，都是实测出来的：
   *  1. 绘图区 `margin.left` 取负值 → 整个绘图区左移，刻度画到容器外；
   *  2. `className` 传 `text-[10px]` 不生效 —— Recharts v3 把 className 加在轴的
   *     `<g>` 上，SVG `<text>` 回落到浏览器默认 16px，`100%` 宽 38px 撑爆 40px 的
   *     Y 轴槽，时间标签也顶出 X 轴行高。修法是用 `tick` prop 下发字号。
   *
   * 这里在真实浏览器里量 `getBoundingClientRect()`，把四个页签的每个刻度文字都
   * 卡在 `.recharts-wrapper` 边界内；任何一处回退都会立刻变红。
   */
  it('keeps every axis tick inside the chart wrapper across all tabs', async () => {
    const history: HardwareHistoryPoint[] = Array.from(
      { length: 30 },
      (_, i) => ({
        timestamp: new Date(Date.UTC(2026, 10, 5, 12, 0, i * 3)).toISOString(),
        cpuUsagePercent: 20 + (i % 15),
        memUsagePercent: 45 + (i % 10),
        npuLoadPercent: 95,
        netRxRate: 1024 * 1024 * (1 + (i % 5)),
        netTxRate: 512 * 1024,
        temperature: 55 + (i % 8),
      })
    )

    const screen = await render(
      <div data-testid='host' style={{ width: '900px' }}>
        <TrendCharts history={history} npuSupported />
      </div>
    )
    const host = screen.getByTestId('host').element()

    const TABS = [
      /计算负载|Compute/,
      /内存|Memory/,
      /网络|Network/,
      /温度|Thermal/,
    ]

    for (const tabPattern of TABS) {
      const tab = await vi.waitFor(() => {
        const found = Array.from(
          host.querySelectorAll<HTMLElement>('[role="tab"]')
        ).find((el) => tabPattern.test(el.textContent ?? ''))
        if (!found) {
          throw new Error(
            `找不到页签 ${tabPattern}；实际 = ${JSON.stringify(
              Array.from(
                host.querySelectorAll<HTMLElement>('[role="tab"]')
              ).map((el) => el.textContent)
            )}`
          )
        }
        return found
      })
      tab.click()

      const wrapper = await vi.waitFor(() => {
        const el = host.querySelector('.recharts-wrapper')
        if (!el) throw new Error('recharts wrapper 尚未挂载')
        return el as HTMLElement
      })

      // 等 Recharts 把刻度注进 DOM
      await vi.waitFor(() => {
        if (wrapper.querySelectorAll('svg text').length === 0) {
          throw new Error('轴刻度尚未渲染')
        }
      })

      const wRect = wrapper.getBoundingClientRect()
      expect(wRect.width).toBeGreaterThan(0)

      for (const node of Array.from(wrapper.querySelectorAll('svg text'))) {
        const box = node.getBoundingClientRect()
        const tag = `"${node.textContent?.trim()}"`
        expect(box.left, `${tag} 左溢出`).toBeGreaterThanOrEqual(
          wRect.left - 0.5
        )
        expect(box.right, `${tag} 右溢出`).toBeLessThanOrEqual(
          wRect.right + 0.5
        )
        expect(box.top, `${tag} 上溢出`).toBeGreaterThanOrEqual(wRect.top - 0.5)
        expect(box.bottom, `${tag} 下溢出`).toBeLessThanOrEqual(
          wRect.bottom + 0.5
        )
      }
    }
  })

  it('renders Y axis ticks at a legible axis font size', async () => {
    const history: HardwareHistoryPoint[] = [
      {
        timestamp: new Date(Date.UTC(2026, 10, 5, 12, 0, 0)).toISOString(),
        cpuUsagePercent: 42,
        memUsagePercent: 50,
        npuLoadPercent: 10,
        netRxRate: 1024,
        netTxRate: 512,
        temperature: 55,
      },
    ]

    const screen = await render(
      <div data-testid='host' style={{ width: '900px' }}>
        <TrendCharts history={history} npuSupported />
      </div>
    )
    const host = screen.getByTestId('host').element()

    const topTick = await vi.waitFor(() => {
      const node = Array.from(host.querySelectorAll('svg text')).find(
        (n) => n.textContent?.trim() === '100%'
      )
      if (!node) throw new Error('Y 轴 100% 刻度尚未渲染')
      return node as SVGTextElement
    })

    // 字号由 `tick` prop 下发（12px 图表刻度常规尺寸）。
    // 退回 className 继承会变成浏览器默认的 16px；误改成 10px 则过小难读。
    expect(getComputedStyle(topTick).fontSize).toBe('12px')
    expect(getComputedStyle(topTick).fontWeight).toBe('500')
    expect(topTick.getBoundingClientRect().width).toBeGreaterThan(0)
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
