import { Cpu, HardDrive, Network, Zap, MemoryStick } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  type CPUStatus,
  type MemoryStatus,
  type NPUStatus,
  type NetworkInterfaceStatus,
  type StorageMountStatus,
} from '../types'
import {
  formatBytes,
  formatRate,
  getProgressIndicatorClass,
  getUsageStatusColor,
} from '../utils/format'

interface StatCardsProps {
  cpu: CPUStatus
  memory: MemoryStatus
  npu: NPUStatus
  network: NetworkInterfaceStatus[]
  storage: StorageMountStatus[]
}

export function StatCards({
  cpu,
  memory,
  npu,
  network,
  storage,
}: StatCardsProps) {
  const { t } = useTranslation('system')

  // Aggregate network rates
  const totalRxRate = network.reduce((acc, curr) => acc + curr.rxBytesRate, 0)
  const totalTxRate = network.reduce((acc, curr) => acc + curr.txBytesRate, 0)

  // Find root or primary mount
  const rootStorage =
    storage.find((s) => s.mountPoint === '/') || storage[0] || null

  return (
    <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5'>
      {/* 1. CPU Card */}
      <Card className='gap-3 p-4'>
        <CardHeader className='flex-row items-center justify-between gap-2 space-y-0 p-0'>
          <CardTitle className='truncate text-sm font-medium text-muted-foreground'>
            {t('cards.cpu', { defaultValue: 'CPU Utilization' })}
          </CardTitle>
          <Cpu className='size-4 shrink-0 text-muted-foreground' />
        </CardHeader>
        <CardContent className='space-y-2 p-0'>
          <div className='flex items-baseline justify-between gap-2'>
            <span
              className={`truncate font-mono text-2xl font-bold tracking-tight ${getUsageStatusColor(
                cpu.usagePercent
              )}`}
            >
              {cpu.usagePercent.toFixed(1)}%
            </span>
            <span className='shrink-0 font-mono text-xs text-muted-foreground'>
              {cpu.temperature > 0 ? `${cpu.temperature.toFixed(1)}°C` : ''}
            </span>
          </div>

          <div
            role='progressbar'
            aria-valuenow={cpu.usagePercent}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('cards.cpu', { defaultValue: 'CPU Utilization' })}
            className='h-2 w-full overflow-hidden rounded-full bg-muted'
          >
            <div
              className={`h-full transition-all duration-500 ${getProgressIndicatorClass(
                cpu.usagePercent
              )}`}
              style={{
                width: `${Math.min(100, Math.max(0, cpu.usagePercent))}%`,
              }}
            />
          </div>

          {cpu.coreUsages && cpu.coreUsages.length > 1 && (
            <div
              className='flex items-center gap-1 pt-0.5'
              title={`Cores: ${cpu.coreUsages.map((u, i) => `#${i}: ${u.toFixed(0)}%`).join(' | ')}`}
            >
              {cpu.coreUsages.map((usage, idx) => (
                <div
                  key={idx}
                  className='h-1.5 flex-1 overflow-hidden rounded-full bg-muted/60'
                >
                  <div
                    className={`h-full transition-all duration-300 ${getProgressIndicatorClass(
                      usage
                    )}`}
                    style={{
                      width: `${Math.min(100, Math.max(0, usage))}%`,
                    }}
                  />
                </div>
              ))}
            </div>
          )}

          <CardDescription className='truncate text-xs'>
            {t('cards.cpuDesc', {
              cores: cpu.cores || 1,
              temp: cpu.temperature.toFixed(1),
              defaultValue: `${cpu.cores || 1} Cores | ${cpu.temperature.toFixed(1)}°C`,
            })}
          </CardDescription>
        </CardContent>
      </Card>

      {/* 2. Memory Card */}
      <Card className='gap-3 p-4'>
        <CardHeader className='flex-row items-center justify-between gap-2 space-y-0 p-0'>
          <CardTitle className='truncate text-sm font-medium text-muted-foreground'>
            {t('cards.memory', { defaultValue: 'Physical RAM' })}
          </CardTitle>
          <MemoryStick className='size-4 shrink-0 text-muted-foreground' />
        </CardHeader>
        <CardContent className='space-y-2 p-0'>
          <div className='flex items-baseline justify-between gap-2'>
            <span
              className={`truncate font-mono text-2xl font-bold tracking-tight ${getUsageStatusColor(
                memory.usedPercent
              )}`}
            >
              {memory.usedPercent.toFixed(1)}%
            </span>
            <span className='shrink-0 font-mono text-xs text-muted-foreground'>
              {formatBytes(memory.usedBytes)}
            </span>
          </div>

          <div
            role='progressbar'
            aria-valuenow={memory.usedPercent}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('cards.memory', { defaultValue: 'Physical RAM' })}
            className='h-2 w-full overflow-hidden rounded-full bg-muted'
          >
            <div
              className={`h-full transition-all duration-500 ${getProgressIndicatorClass(
                memory.usedPercent
              )}`}
              style={{
                width: `${Math.min(100, Math.max(0, memory.usedPercent))}%`,
              }}
            />
          </div>

          <CardDescription className='truncate text-xs'>
            {t('cards.memoryDesc', {
              used: formatBytes(memory.usedBytes),
              total: formatBytes(memory.totalBytes),
              defaultValue: `Used ${formatBytes(memory.usedBytes)} / ${formatBytes(memory.totalBytes)}`,
            })}
          </CardDescription>
        </CardContent>
      </Card>

      {/* 3. NPU Card */}
      <Card className='gap-3 p-4'>
        <CardHeader className='flex-row items-center justify-between gap-2 space-y-0 p-0'>
          <CardTitle className='truncate text-sm font-medium text-muted-foreground'>
            {t('cards.npu', { defaultValue: 'NPU Coprocessor' })}
          </CardTitle>
          <Zap className='size-4 shrink-0 text-muted-foreground' />
        </CardHeader>
        <CardContent className='space-y-2 p-0'>
          <div className='flex items-baseline justify-between gap-2'>
            {npu.supported ? (
              <>
                <span
                  className={`truncate font-mono text-2xl font-bold tracking-tight ${getUsageStatusColor(
                    npu.loadPercent
                  )}`}
                >
                  {npu.loadPercent.toFixed(1)}%
                </span>
                <span className='shrink-0 font-mono text-xs text-muted-foreground'>
                  {npu.temperature > 0 ? `${npu.temperature.toFixed(1)}°C` : ''}
                </span>
              </>
            ) : (
              <span className='text-lg font-medium text-muted-foreground'>
                N/A
              </span>
            )}
          </div>

          <div
            role='progressbar'
            aria-valuenow={npu.supported ? npu.loadPercent : 0}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('cards.npu', { defaultValue: 'NPU Coprocessor' })}
            className='h-2 w-full overflow-hidden rounded-full bg-muted'
          >
            <div
              className={`h-full transition-all duration-500 ${
                npu.supported
                  ? getProgressIndicatorClass(npu.loadPercent)
                  : 'bg-muted'
              }`}
              style={{
                width: `${
                  npu.supported
                    ? Math.min(100, Math.max(0, npu.loadPercent))
                    : 0
                }%`,
              }}
            />
          </div>

          {npu.supported && npu.coreLoads && npu.coreLoads.length > 1 && (
            <div
              className='flex items-center gap-1 pt-0.5'
              title={`NPU Cores: ${npu.coreLoads.map((l, i) => `Core${i}: ${l.toFixed(0)}%`).join(' | ')}`}
            >
              {npu.coreLoads.map((load, idx) => (
                <div
                  key={idx}
                  className='h-1.5 flex-1 overflow-hidden rounded-full bg-muted/60'
                >
                  <div
                    className={`h-full transition-all duration-300 ${getProgressIndicatorClass(
                      load
                    )}`}
                    style={{
                      width: `${Math.min(100, Math.max(0, load))}%`,
                    }}
                  />
                </div>
              ))}
            </div>
          )}

          <CardDescription className='truncate text-xs'>
            {!npu.supported
              ? t('cards.npuUnsupported', {
                  defaultValue: 'No NPU coprocessor detected',
                })
              : npu.loadPercent === 0
                ? t('cards.npuNoLoad', {
                    defaultValue: 'Idle (No active inference models)',
                  })
                : t('cards.npuDesc', {
                    name: npu.name || 'NPU',
                    temp: npu.temperature.toFixed(1),
                    defaultValue: `${npu.name} | ${npu.temperature.toFixed(1)}°C`,
                  })}
          </CardDescription>
        </CardContent>
      </Card>

      {/* 4. Network Card */}
      <Card className='gap-3 p-4'>
        <CardHeader className='flex-row items-center justify-between gap-2 space-y-0 p-0'>
          <CardTitle className='truncate text-sm font-medium text-muted-foreground'>
            {t('cards.network', { defaultValue: 'Network Rates' })}
          </CardTitle>
          <Network className='size-4 shrink-0 text-muted-foreground' />
        </CardHeader>
        <CardContent className='space-y-2 p-0'>
          <div className='flex items-baseline justify-between gap-2'>
            <span className='truncate font-mono text-2xl font-bold tracking-tight text-primary'>
              {formatRate(totalRxRate)}
            </span>
            <span className='shrink-0 font-mono text-xs text-muted-foreground'>
              ↑ {formatRate(totalTxRate)}
            </span>
          </div>

          <div
            role='progressbar'
            aria-valuenow={totalRxRate + totalTxRate > 0 ? 100 : 0}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('cards.network', { defaultValue: 'Network Rates' })}
            className='flex h-2 w-full gap-1 overflow-hidden rounded-full bg-muted'
          >
            {totalRxRate + totalTxRate > 0 ? (
              <>
                <div
                  className='h-full bg-primary transition-all duration-500'
                  style={{
                    width: `${Math.max(
                      10,
                      (totalRxRate / (totalRxRate + totalTxRate)) * 100
                    )}%`,
                  }}
                  title={`Rx: ${formatRate(totalRxRate)}`}
                />
                <div
                  className='h-full bg-emerald-500 transition-all duration-500'
                  style={{
                    width: `${Math.max(
                      10,
                      (totalTxRate / (totalRxRate + totalTxRate)) * 100
                    )}%`,
                  }}
                  title={`Tx: ${formatRate(totalTxRate)}`}
                />
              </>
            ) : (
              <div className='h-full w-full bg-muted' />
            )}
          </div>

          <CardDescription className='truncate text-xs'>
            {t('cards.networkDesc', {
              rx: formatRate(totalRxRate),
              tx: formatRate(totalTxRate),
              defaultValue: `↓ ${formatRate(totalRxRate)} | ↑ ${formatRate(totalTxRate)}`,
            })}
          </CardDescription>
        </CardContent>
      </Card>

      {/* 5. Storage Card */}
      <Card className='gap-3 p-4'>
        <CardHeader className='flex-row items-center justify-between gap-2 space-y-0 p-0'>
          <CardTitle className='truncate text-sm font-medium text-muted-foreground'>
            {t('cards.storage', { defaultValue: 'Primary Storage' })}
          </CardTitle>
          <HardDrive className='size-4 shrink-0 text-muted-foreground' />
        </CardHeader>
        <CardContent className='space-y-2 p-0'>
          <div className='flex items-baseline justify-between gap-2'>
            <span
              className={`truncate font-mono text-2xl font-bold tracking-tight ${getUsageStatusColor(
                rootStorage?.usedPercent ?? 0
              )}`}
            >
              {(rootStorage?.usedPercent ?? 0).toFixed(1)}%
            </span>
            <span className='shrink-0 font-mono text-xs text-muted-foreground'>
              {rootStorage ? formatBytes(rootStorage.usedBytes) : '-'}
            </span>
          </div>

          <div
            role='progressbar'
            aria-valuenow={rootStorage?.usedPercent ?? 0}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('cards.storage', { defaultValue: 'Primary Storage' })}
            className='h-2 w-full overflow-hidden rounded-full bg-muted'
          >
            <div
              className={`h-full transition-all duration-500 ${getProgressIndicatorClass(
                rootStorage?.usedPercent ?? 0
              )}`}
              style={{
                width: `${Math.min(
                  100,
                  Math.max(0, rootStorage?.usedPercent ?? 0)
                )}%`,
              }}
            />
          </div>

          <CardDescription className='truncate text-xs'>
            {rootStorage
              ? t('cards.storageDesc', {
                  used: formatBytes(rootStorage.usedBytes),
                  free: formatBytes(rootStorage.freeBytes),
                  defaultValue: `Used ${formatBytes(rootStorage.usedBytes)} / Free ${formatBytes(rootStorage.freeBytes)}`,
                })
              : '-'}
          </CardDescription>
        </CardContent>
      </Card>
    </div>
  )
}
