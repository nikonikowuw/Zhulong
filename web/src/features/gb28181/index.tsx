import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { AlertCircle, Plus, RefreshCw, Server, Sliders } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import useDialogState from '@/hooks/use-dialog-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ContentSection } from '@/components/layout/content-section'
import { Gb28181ChannelsDrawer } from './components/gb28181-channels-drawer'
import { Gb28181ConfigForm } from './components/gb28181-config-form'
import { Gb28181DeviceDeleteDialog } from './components/gb28181-device-delete-dialog'
import { Gb28181DeviceDialog } from './components/gb28181-device-dialog'
import { Gb28181DevicesTable } from './components/gb28181-devices-table'
import { Gb28181RuntimePanel } from './components/gb28181-runtime-panel'
import {
  type Gb28181Config,
  type Gb28181Device,
  type UpsertGb28181DeviceRequest,
} from './data/schema'
import {
  GB28181_QUERY_KEYS,
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
 * 页面采用双层架构：
 * 1. 顶部现场 Cockpit 仪表盘：秒级展示服务运行状态、在线/会话核心容量与底层防护计数；
 * 2. 主体选项卡解耦：将高频日常运维（设备白名单与通道同步）与低频服务配置解耦，
 *    解决以往长表单下压列表、动线混乱的问题。
 */
export function SettingsGb28181() {
  const { t } = useTranslation('gb28181')
  const queryClient = useQueryClient()
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const [activeTab, setActiveTab] = useState<'devices' | 'config'>('devices')

  const configQuery = useGb28181Config()
  const runtimeQuery = useGb28181Runtime()
  const devicesQuery = useGb28181Devices()
  const updateConfig = useUpdateGb28181Config()
  const upsertDevice = useUpsertGb28181Device()
  const deleteDevice = useDeleteGb28181Device()
  const { mutateAsync: updateConfigAsync } = updateConfig
  const { mutateAsync: upsertDeviceAsync } = upsertDevice

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

  const handleViewChannels = useCallback(
    (device: Gb28181Device) => {
      setChannelsDevice(device)
      setChannelsDrawerOpen(true)
    },
    [setChannelsDrawerOpen]
  )

  const handleChannelsDrawerOpenChange = useCallback(
    (open: boolean | null) => {
      setChannelsDrawerOpen(open)
      if (!open) {
        setChannelsDevice(null)
      }
    },
    [setChannelsDrawerOpen]
  )

  const handleRefresh = useCallback(() => {
    void queryClient.refetchQueries({ queryKey: GB28181_QUERY_KEYS.all })
  }, [queryClient])

  const handleRetryDevices = useCallback(() => {
    void queryClient.refetchQueries({
      queryKey: GB28181_QUERY_KEYS.devices(),
      exact: true,
    })
  }, [queryClient])

  const handleAddDevice = useCallback(() => {
    setEditingDevice(null)
    setDeviceDialogOpen(true)
  }, [setDeviceDialogOpen])

  const handleEditDevice = useCallback(
    (device: Gb28181Device) => {
      setEditingDevice(device)
      setDeviceDialogOpen(true)
    },
    [setDeviceDialogOpen]
  )

  const handleDeleteDevice = useCallback(
    (device: Gb28181Device) => {
      setDeletingDevice(device)
      setDeleteDialogOpen(true)
    },
    [setDeleteDialogOpen]
  )

  const handleDeleteDialogOpenChange = useCallback(
    (open: boolean) => {
      setDeleteDialogOpen(open)
      if (!open) setDeletingDevice(null)
    },
    [setDeleteDialogOpen]
  )

  const handleSaveConfig = useCallback(
    (payload: Gb28181Config) => updateConfigAsync(payload),
    [updateConfigAsync]
  )

  const handleUpsertDevice = useCallback(
    async (deviceId: string, payload: UpsertGb28181DeviceRequest) => {
      await upsertDeviceAsync({ deviceId, payload })
    },
    [upsertDeviceAsync]
  )

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
  const restartRequired =
    runtimeQuery.data?.restartRequired ?? runtimeInfo?.restartRequired ?? false
  const deviceCount = devicesQuery.data?.length

  return (
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='w-full'
    >
      <div className='space-y-5'>
        {/* 顶部运行态排障 Cockpit 面板 */}
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

        {/* 主体：Tabs 分组解耦 */}
        <Tabs
          value={activeTab}
          onValueChange={(val) => setActiveTab(val as 'devices' | 'config')}
          className='w-full space-y-4'
        >
          {/* 工具栏：分栏切换 + 主操作 */}
          <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
            <TabsList className='h-9 bg-muted/70 p-1'>
              <TabsTrigger value='devices' className='gap-2 px-3 text-xs'>
                <Server className='size-3.5' />
                <span>{t('sections.devices')}</span>
                {typeof deviceCount === 'number' && (
                  <span className='py-0.2 rounded-full bg-muted-foreground/15 px-1.5 font-mono text-[11px] leading-tight text-foreground'>
                    {deviceCount}
                  </span>
                )}
              </TabsTrigger>
              <TabsTrigger value='config' className='gap-2 px-3 text-xs'>
                <Sliders className='size-3.5' />
                <span>{t('sections.config')}</span>
                {restartRequired && (
                  <span
                    className={cn(
                      'size-1.5 animate-pulse rounded-full',
                      statusDotClass('warning')
                    )}
                  />
                )}
              </TabsTrigger>
            </TabsList>

            {activeTab === 'devices' && (
              <Button
                size='sm'
                onClick={handleAddDevice}
                className='h-8 gap-1.5 self-start text-xs shadow-2xs sm:self-auto'
              >
                <Plus className='size-3.5' />
                {t('devices.addTitle')}
              </Button>
            )}
          </div>

          {/* 选项卡一：设备白名单与通道管理 */}
          <TabsContent value='devices' className='space-y-4 outline-none'>
            <Gb28181DevicesTable
              devices={devicesQuery.data ?? []}
              isLoading={devicesQuery.isLoading}
              isError={devicesQuery.isError}
              errorMessage={
                devicesQuery.error
                  ? describeGb28181Error(devicesQuery.error, t)
                  : undefined
              }
              onRetry={handleRetryDevices}
              onAdd={handleAddDevice}
              onEdit={handleEditDevice}
              onViewChannels={handleViewChannels}
              onDelete={handleDeleteDevice}
              search={search}
              navigate={navigate}
            />
          </TabsContent>

          {/* 选项卡二：接入服务目标配置 */}
          <TabsContent
            value='config'
            forceMount
            hidden={activeTab !== 'config'}
            className='space-y-4 outline-none'
          >
            {configQuery.isLoading ? (
              <div className='space-y-3 rounded-xl border border-border/70 bg-card/50 p-4'>
                <Skeleton className='h-4 w-32' />
                <Skeleton className='h-9 w-full' />
                <Skeleton className='h-9 w-full' />
                <Skeleton className='h-9 w-2/3' />
              </div>
            ) : configQuery.isError || !config || !runtimeInfo ? (
              <Alert className={statusLevelClass('destructive')}>
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
                onSave={handleSaveConfig}
              />
            )}
          </TabsContent>
        </Tabs>

        {/* 弹窗与抽屉（同构生命周期管理） */}
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
          onSubmit={handleUpsertDevice}
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
