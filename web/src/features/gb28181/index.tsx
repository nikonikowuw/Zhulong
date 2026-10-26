import { useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { AlertCircle, Plus, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import useDialogState from '@/hooks/use-dialog-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { ContentSection } from '@/components/layout/content-section'
import { Gb28181ConfigForm } from './components/gb28181-config-form'
import { Gb28181DeviceDialog } from './components/gb28181-device-dialog'
import { Gb28181DevicesTable } from './components/gb28181-devices-table'
import { Gb28181RuntimePanel } from './components/gb28181-runtime-panel'
import { type Gb28181Device } from './data/schema'
import {
  useGb28181Config,
  useGb28181Devices,
  useGb28181Runtime,
  useUpdateGb28181Config,
  useUpsertGb28181Device,
} from './hooks/use-gb28181'
import { describeGb28181Error } from './utils/error-message'

const route = getRouteApi('/_authenticated/settings/gb28181')

/**
 * GB28181 接入设置页。
 *
 * 页面职责是「让值守的人看清三件事」：本进程在跑什么、已保存的配置与之
 * 差在哪、哪些设备被允许注册。它不负责触目录查询或点播 —— 那一层在
 * 后端 S2/S4 落地后才会有对应端点。
 */
export function SettingsGb28181() {
  const { t } = useTranslation('gb28181')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const configQuery = useGb28181Config()
  const runtimeQuery = useGb28181Runtime()
  const devicesQuery = useGb28181Devices()
  const updateConfig = useUpdateGb28181Config()
  const upsertDevice = useUpsertGb28181Device()

  // 弹窗开关走仓库统一的 `use-dialog-state`（规范 §1.8），
  // 不用裸 `useState<boolean>`：两种情况写反一颗布尔值时行为必须一致。
  const [deviceDialogOpen, setDeviceDialogOpen] = useDialogState<boolean>(false)
  const [editingDevice, setEditingDevice] = useState<Gb28181Device | null>(null)

  const handleRefresh = () => {
    void configQuery.refetch()
    void runtimeQuery.refetch()
    void devicesQuery.refetch()
  }

  const handleAddDevice = () => {
    setEditingDevice(null)
    setDeviceDialogOpen(true)
  }

  const handleEditDevice = (device: Gb28181Device) => {
    setEditingDevice(device)
    setDeviceDialogOpen(true)
  }

  const config = configQuery.data?.config
  const runtimeInfo = configQuery.data?.runtime

  return (
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='max-w-5xl'
    >
      <div className='space-y-6'>
        <Gb28181RuntimePanel
          runtimeInfo={runtimeInfo}
          snapshot={runtimeQuery.data}
          isLoading={configQuery.isLoading || runtimeQuery.isLoading}
          isError={configQuery.isError || runtimeQuery.isError}
          errorMessage={
            configQuery.error
              ? describeGb28181Error(configQuery.error, t)
              : runtimeQuery.error
                ? describeGb28181Error(runtimeQuery.error, t)
                : undefined
          }
          isFetching={configQuery.isFetching || runtimeQuery.isFetching}
          onRetry={handleRefresh}
        />

        <Separator />

        {configQuery.isLoading ? (
          <div className='space-y-3'>
            <Skeleton className='h-4 w-32' />
            <Skeleton className='h-9 w-full' />
            <Skeleton className='h-9 w-full' />
            <Skeleton className='h-9 w-2/3' />
          </div>
        ) : configQuery.isError || !config || !runtimeInfo ? (
          <Alert className='border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20'>
            <AlertCircle className='h-4 w-4' />
            <AlertTitle className='flex items-center justify-between gap-2 text-sm font-semibold'>
              {t('config.loadError')}
              <Button
                variant='outline'
                size='sm'
                onClick={() => void configQuery.refetch()}
                className='h-7 gap-1 text-xs'
              >
                <RefreshCw className='h-3 w-3' />
                {t('actions.retry')}
              </Button>
            </AlertTitle>
            <AlertDescription className='text-xs'>
              {configQuery.error
                ? describeGb28181Error(configQuery.error, t)
                : t('errors.unknown')}
            </AlertDescription>
          </Alert>
        ) : (
          <Gb28181ConfigForm
            config={config}
            runtime={runtimeInfo}
            isSaving={updateConfig.isPending}
            onSave={(payload) => updateConfig.mutateAsync(payload)}
          />
        )}

        <Separator />

        <section className='space-y-3'>
          <div className='flex flex-wrap items-start justify-between gap-2'>
            <div className='space-y-1'>
              <h3 className='text-sm font-semibold'>{t('sections.devices')}</h3>
              <p className='text-xs text-muted-foreground'>
                {t('devices.desc')}
              </p>
            </div>
            <Button
              size='sm'
              onClick={handleAddDevice}
              className='h-8 gap-1.5 text-xs'
            >
              <Plus className='h-3.5 w-3.5' />
              {t('devices.addTitle')}
            </Button>
          </div>

          <Gb28181DevicesTable
            devices={devicesQuery.data ?? []}
            isLoading={devicesQuery.isLoading}
            isError={devicesQuery.isError}
            errorMessage={
              devicesQuery.error
                ? describeGb28181Error(devicesQuery.error, t)
                : undefined
            }
            onRetry={() => void devicesQuery.refetch()}
            onEdit={handleEditDevice}
            search={search}
            navigate={navigate}
          />
        </section>

        <Gb28181DeviceDialog
          open={deviceDialogOpen ?? false}
          device={editingDevice}
          isPending={upsertDevice.isPending}
          onOpenChange={setDeviceDialogOpen}
          onSubmit={async (deviceId, payload) => {
            await upsertDevice.mutateAsync({ deviceId, payload })
          }}
        />
      </div>
    </ContentSection>
  )
}
