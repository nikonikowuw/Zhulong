import { useState } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ContentSection } from '../components/content-section'
import { DeviceIdentityCard } from './components/device-identity-card'
import { NetworkTable } from './components/network-table'
import { StatCards } from './components/stat-cards'
import { StorageTable } from './components/storage-table'
import { SystemHeader } from './components/system-header'
import { ThermalList } from './components/thermal-list'
import { TrendCharts } from './components/trend-charts'
import { useSystemHardware } from './hooks/use-system'

export function SettingsSystem() {
  const { t } = useTranslation('system')
  const [isPaused, setIsPaused] = useState(false)
  const {
    data: hardware,
    isLoading,
    isRefetching,
    error,
    refetch,
  } = useSystemHardware({ isPaused })

  if (isLoading) {
    return (
      <ContentSection
        title={t('page.title', { defaultValue: '系统状态与算力遥测' })}
        desc={t('page.desc', {
          defaultValue:
            '实时监测边缘主机 CPU、物理内存、磁盘存储、网络速率以及 NPU 异构加速算力与核心温度趋势。',
        })}
        className='w-full max-w-none'
      >
        <div className='space-y-6'>
          <Skeleton className='h-16 w-full rounded-xl' />
          <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5'>
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className='h-32 rounded-xl' />
            ))}
          </div>
          <Skeleton className='h-64 rounded-xl' />
          <Skeleton className='h-48 rounded-xl' />
        </div>
      </ContentSection>
    )
  }

  if (error || !hardware) {
    return (
      <ContentSection
        title={t('page.title', { defaultValue: '系统状态与算力遥测' })}
        desc={t('page.desc', {
          defaultValue:
            '实时监测边缘主机 CPU、物理内存、磁盘存储、网络速率以及 NPU 异构加速算力与核心温度趋势。',
        })}
        className='w-full max-w-none'
      >
        <div className='flex flex-col items-center justify-center gap-3 rounded-xl border border-destructive/20 bg-destructive/5 p-12 text-center'>
          <AlertCircle className='size-8 text-destructive' />
          <p className='text-sm font-medium text-destructive'>
            {error instanceof Error
              ? error.message
              : 'Failed to load telemetry'}
          </p>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void refetch()}
            className='gap-1.5'
          >
            <RefreshCw className='size-3.5' />
            {t('header.refresh', { defaultValue: 'Retry' })}
          </Button>
        </div>
      </ContentSection>
    )
  }

  return (
    <ContentSection
      title={t('page.title', { defaultValue: '系统状态与算力遥测' })}
      desc={t('page.desc', {
        defaultValue:
          '实时监测边缘主机 CPU、物理内存、磁盘存储、网络速率以及 NPU 异构加速算力与核心温度趋势。',
      })}
      className='w-full max-w-none'
    >
      <div className='space-y-6'>
        {/* 1. Device identity (device_id stamped onto every pushed event) */}
        <DeviceIdentityCard />

        {/* 2. Host identity & live refresh bar */}
        <SystemHeader
          host={hardware.host}
          isRefetching={isRefetching}
          isPaused={isPaused}
          onTogglePause={() => setIsPaused((prev) => !prev)}
          onRefresh={() => void refetch()}
        />

        {/* 3. Top Metric Cards (CPU, RAM, NPU, Network, Storage) */}
        <StatCards
          cpu={hardware.cpu}
          memory={hardware.memory}
          npu={hardware.heterogeneous.npu}
          network={hardware.network}
          storage={hardware.storage}
        />

        {/* 4. Short-term Trend Charts (Recharts) */}
        <TrendCharts
          history={hardware.history}
          npuSupported={hardware.heterogeneous.npu.supported}
        />

        {/* 5. Hardware Detail Tables (Mounts & Network) */}
        <div className='grid grid-cols-1 gap-6 lg:grid-cols-2'>
          <StorageTable storage={hardware.storage} />
          <NetworkTable network={hardware.network} />
        </div>

        {/* 6. Thermal Sensors */}
        <ThermalList thermals={hardware.heterogeneous.temperatures} />
      </div>
    </ContentSection>
  )
}
