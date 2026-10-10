import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ContentSection } from '../components/content-section'
import { ManualCleanupDialog } from './components/manual-cleanup-dialog'
import { StorageCapacityBar } from './components/storage-capacity-bar'
import { StorageConfigForm } from './components/storage-config-form'
import { useStorageConfig, useStorageStatus } from './hooks/use-storage'

export function SettingsStorage() {
  const { t } = useTranslation('storage')
  const {
    data: status,
    isLoading: isStatusLoading,
    isRefetching: isStatusRefetching,
    refetch: refetchStatus,
  } = useStorageStatus()

  const {
    data: config,
    isLoading: isConfigLoading,
    refetch: refetchConfig,
  } = useStorageConfig()

  const handleRefresh = () => {
    void refetchStatus()
    void refetchConfig()
  }

  const isLoading = isStatusLoading || isConfigLoading

  return (
    <ContentSection
      title={t('page.title', { defaultValue: '存储与生命周期' })}
      desc={t('page.desc', {
        defaultValue:
          '配置边缘媒体存储挂载路径、statfs 水位感知监控、外挂盘掉线防穿透保护以及双水位回差自动清理策略。',
      })}
    >
      <div className='space-y-6'>
        {/* Header Action Row */}
        <div className='flex items-center justify-end gap-2'>
          <Button
            variant='outline'
            size='sm'
            onClick={handleRefresh}
            disabled={isStatusRefetching}
            className='gap-1.5'
          >
            <RefreshCw
              className={`size-3.5 text-muted-foreground ${
                isStatusRefetching ? 'animate-spin' : ''
              }`}
            />
            {t('actions.refresh', { defaultValue: '刷新状态' })}
          </Button>
          <ManualCleanupDialog
            onSuccess={() => {
              void refetchStatus()
            }}
          />
        </div>

        {/* Storage Capacity Bar & Breakdown Overview */}
        {isStatusLoading ? (
          <div className='space-y-3 rounded-lg border p-6'>
            <Skeleton className='h-6 w-1/3' />
            <Skeleton className='h-12 w-full' />
            <Skeleton className='h-4 w-2/3' />
          </div>
        ) : (
          <StorageCapacityBar
            status={status ?? null}
            isLoading={isStatusRefetching}
          />
        )}

        {/* Storage Configuration Form */}
        {isLoading || !config ? (
          <div className='space-y-4 rounded-lg border p-6'>
            <Skeleton className='h-8 w-1/4' />
            <Skeleton className='h-10 w-full' />
            <div className='grid grid-cols-3 gap-4'>
              <Skeleton className='h-10' />
              <Skeleton className='h-10' />
              <Skeleton className='h-10' />
            </div>
          </div>
        ) : (
          <StorageConfigForm
            initialConfig={config}
            status={status}
            onConfigUpdated={handleRefresh}
          />
        )}
      </div>
    </ContentSection>
  )
}
