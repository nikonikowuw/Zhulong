import { useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { AlertCircle, Plus, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import useDialogState from '@/hooks/use-dialog-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { ContentSection } from '@/components/layout/content-section'
import { Gb28181ChannelsDrawer } from './components/gb28181-channels-drawer'
import { Gb28181ConfigForm } from './components/gb28181-config-form'
import { Gb28181DeviceDeleteDialog } from './components/gb28181-device-delete-dialog'
import { Gb28181DeviceDialog } from './components/gb28181-device-dialog'
import { Gb28181DevicesTable } from './components/gb28181-devices-table'
import { Gb28181RuntimePanel } from './components/gb28181-runtime-panel'
import { type Gb28181Device } from './data/schema'
import {
  useGb28181Config,
  useGb28181Devices,
  useGb28181Runtime,
  useDeleteGb28181Device,
  useUpdateGb28181Config,
  useUpsertGb28181Device,
} from './hooks/use-gb28181'
import { describeGb28181Error } from './utils/error-message'

const route = getRouteApi('/_authenticated/settings/gb28181')

/**
 * GB28181 接入设置页。
 *
 * 页面展示运行状态、配置和允许注册的设备，并提供设备目录查看、目录同步与摄像机添加操作。
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
  const deleteDevice = useDeleteGb28181Device()

  // 弹窗开关走仓库统一的 `use-dialog-state`（规范 §1.8），
  // 不用裸 `useState<boolean>`：两种情况写反一颗布尔值时行为必须一致。
  const [deviceDialogOpen, setDeviceDialogOpen] = useDialogState<boolean>(false)
  const [editingDevice, setEditingDevice] = useState<Gb28181Device | null>(null)
  // 通道抽屉与凭据弹窗同构：开关走 `use-dialog-state`，当前行单独持有，
  // 关闭时一并清空，避免下次打开先闪一帧上一台设备。
  const [channelsDrawerOpen, setChannelsDrawerOpen] =
    useDialogState<boolean>(false)
  const [channelsDevice, setChannelsDevice] = useState<Gb28181Device | null>(
    null
  )
  const [deleteDialogOpen, setDeleteDialogOpen] = useDialogState<boolean>(false)
  const [deletingDevice, setDeletingDevice] = useState<Gb28181Device | null>(
    null
  )

  const handleViewChannels = (device: Gb28181Device) => {
    setChannelsDevice(device)
    setChannelsDrawerOpen(true)
  }

  const handleChannelsDrawerOpenChange = (open: boolean | null) => {
    setChannelsDrawerOpen(open)
    if (!open) {
      setChannelsDevice(null)
    }
  }

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

  const handleDeleteDevice = (device: Gb28181Device) => {
    setDeletingDevice(device)
    setDeleteDialogOpen(true)
  }

  const handleDeleteDialogOpenChange = (open: boolean) => {
    setDeleteDialogOpen(open)
    if (!open) setDeletingDevice(null)
  }

  const handleConfirmDelete = async () => {
    const device = deletingDevice
    if (!device || deleteDevice.isPending) return

    try {
      await deleteDevice.mutateAsync(device.deviceId)
      toast.success(t('devices.deleteSuccess', { deviceId: device.deviceId }))
      handleDeleteDialogOpenChange(false)
      if (channelsDevice?.deviceId === device.deviceId) {
        handleChannelsDrawerOpenChange(false)
      }
    } catch (error: unknown) {
      toast.error(describeGb28181Error(error, t))
    }
  }

  const config = configQuery.data?.config
  const runtimeInfo = configQuery.data?.runtime

  return (
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='w-full'
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
            onViewChannels={handleViewChannels}
            onDelete={handleDeleteDevice}
            search={search}
            navigate={navigate}
          />
        </section>

        <Gb28181ChannelsDrawer
          device={channelsDevice}
          open={channelsDrawerOpen ?? false}
          onOpenChange={handleChannelsDrawerOpenChange}
        />

        <Gb28181DeviceDialog
          open={deviceDialogOpen ?? false}
          device={editingDevice}
          isPending={upsertDevice.isPending}
          onOpenChange={setDeviceDialogOpen}
          onSubmit={async (deviceId, payload) => {
            await upsertDevice.mutateAsync({ deviceId, payload })
          }}
        />
        <Gb28181DeviceDeleteDialog
          open={deleteDialogOpen ?? false}
          device={deletingDevice}
          isPending={deleteDevice.isPending}
          onOpenChange={handleDeleteDialogOpenChange}
          onConfirm={handleConfirmDelete}
        />
      </div>
    </ContentSection>
  )
}
