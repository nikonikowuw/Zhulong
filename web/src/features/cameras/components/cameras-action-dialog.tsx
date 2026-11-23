import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { AlertCircle, Loader2, Radar, RefreshCw, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { camerasApi } from '../api/cameras-api'
import {
  createCameraFormSchema,
  type Camera,
  type CameraFormValues,
} from '../data/schema'
import {
  CAMERAS_QUERY_KEY,
  useCreateCamera,
  useUpdateCamera,
} from '../hooks/use-cameras'
import { CamerasActionStreamFields } from './cameras-action-stream-fields'
import { useCamerasContext } from './cameras-provider'

interface CamerasActionDialogBodyProps {
  isEdit: boolean
  camera: Camera | null
  onClose: () => void
  onPendingChange: (pending: boolean) => void
}

function CamerasActionDialogBody({
  isEdit,
  camera,
  onClose,
  onPendingChange,
}: CamerasActionDialogBodyProps) {
  const { t } = useTranslation('cameras')
  const queryClient = useQueryClient()
  const { discoveredDevice } = useCamerasContext()
  const [probeError, setProbeError] = useState<string | null>(null)
  const [conflictError, setConflictError] = useState<string | null>(null)
  const [latestRevision, setLatestRevision] = useState<number | null>(null)
  const [isRefreshingRevision, setIsRefreshingRevision] = useState(false)

  const mainStream = camera?.streams?.find((stream) => stream.role === 'main')
  const subStream = camera?.streams?.find((stream) => stream.role === 'sub')
  const isGb28181 = isEdit && mainStream?.protocol === 'gb28181'

  const createMutation = useCreateCamera()
  const updateMutation = useUpdateCamera()
  const isPending = createMutation.isPending || updateMutation.isPending

  const schema = useMemo(
    () => createCameraFormSchema(t, !isGb28181),
    [t, isGb28181]
  )

  const form = useForm<CameraFormValues>({
    resolver: zodResolver(schema),
    defaultValues:
      isEdit && camera
        ? {
            name: camera.name,
            enabled: camera.enabled,
            mainRtspUrl: isGb28181 ? '' : mainStream?.rtspUrl || '',
            mainTransport: (mainStream?.transport as 'tcp' | 'udp') || 'tcp',
            hasSubStream: !isGb28181 && Boolean(subStream),
            subRtspUrl: isGb28181 ? '' : subStream?.rtspUrl || '',
            subTransport: (subStream?.transport as 'tcp' | 'udp') || 'tcp',
          }
        : {
            name:
              discoveredDevice?.name ||
              discoveredDevice?.model ||
              (discoveredDevice?.ip ? `Camera-${discoveredDevice.ip}` : ''),
            enabled: true,
            mainRtspUrl:
              discoveredDevice?.suggestedRtspUrl ||
              (discoveredDevice?.ip
                ? `rtsp://admin:password@${discoveredDevice.ip}:554/live/ch0`
                : ''),
            mainTransport: 'tcp',
            hasSubStream: false,
            subRtspUrl: '',
            subTransport: 'tcp',
          },
  })

  const handleRefreshRevision = async () => {
    if (!camera?.id || isRefreshingRevision) return
    setIsRefreshingRevision(true)
    try {
      const fresh = await camerasApi.getCamera(camera.id)
      setLatestRevision(fresh.revision)
      setConflictError(null)
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
    } catch {
      toast.error(t('toast.refreshLatestFailed'))
    } finally {
      setIsRefreshingRevision(false)
    }
  }

  const onSubmit = async (values: CameraFormValues) => {
    if (isPending) return
    setProbeError(null)
    setConflictError(null)
    onPendingChange(true)

    const payload = { name: values.name, enabled: values.enabled }
    const streamPayload = {
      mainStream: {
        role: 'main' as const,
        protocol: 'rtsp',
        rtspUrl: values.mainRtspUrl,
        transport: values.mainTransport,
      },
      subStream: values.hasSubStream
        ? {
            role: 'sub' as const,
            protocol: 'rtsp',
            rtspUrl: values.subRtspUrl || '',
            transport: values.subTransport,
          }
        : isEdit && camera?.streams?.some((s) => s.role === 'sub')
          ? {
              role: 'sub' as const,
              protocol: 'rtsp',
              rtspUrl: '',
              transport: 'tcp' as const,
            }
          : undefined,
    }

    try {
      if (isEdit && camera) {
        await updateMutation.mutateAsync({
          id: camera.id,
          data: {
            revision: latestRevision ?? camera.revision,
            ...payload,
            ...(isGb28181 ? {} : streamPayload),
          },
        })
        toast.success(t('toast.updateSuccess'))
      } else {
        await createMutation.mutateAsync({ ...payload, ...streamPayload })
        toast.success(t('toast.createSuccess'))
      }
      onClose()
    } catch (err: unknown) {
      handleFormError(form, err, {
        fieldMap: {
          'mainStream.rtspUrl': 'mainRtspUrl',
          'subStream.rtspUrl': 'subRtspUrl',
          'mainStream.transport': 'mainTransport',
          'subStream.transport': 'subTransport',
        },
        onConflict: (_err, message) => {
          setConflictError(message || t('toast.casConflict'))
        },
        onUnhandled: (message) => {
          setProbeError(message)
        },
      })
    } finally {
      onPendingChange(false)
    }
  }

  return (
    <>
      <SheetHeader className='shrink-0 gap-2 border-b px-6 py-4 pe-14 text-start'>
        <div className='flex items-center gap-3'>
          <div className='flex size-10 shrink-0 items-center justify-center rounded-xl border border-border/80 bg-muted/50 text-primary shadow-2xs'>
            <Video className='size-5' />
          </div>
          <div className='min-w-0 space-y-1'>
            <SheetTitle className='text-base font-semibold tracking-tight'>
              {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
            </SheetTitle>
            <SheetDescription className='text-xs text-muted-foreground'>
              {isGb28181
                ? t('dialog.gb28181EditDesc')
                : isEdit
                  ? t('dialog.editDesc')
                  : t('dialog.createDesc')}
            </SheetDescription>
          </div>
        </div>
      </SheetHeader>

      <div
        data-testid='cameras-action-scroll'
        className='min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4'
      >
        {!isEdit && discoveredDevice ? (
          <div className='flex items-center gap-2 rounded-lg border border-primary/20 bg-primary/5 p-2.5 text-xs text-primary'>
            <Radar className='size-4 shrink-0' />
            <span>
              {t('discover.prefilledTip', { ip: discoveredDevice.ip })}
            </span>
          </div>
        ) : null}

        {conflictError ? (
          <div
            className={cn(
              'flex items-start gap-2.5 rounded-lg border p-3 text-sm',
              STATUS_LEVEL_CLASS.warning
            )}
          >
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{conflictError}</div>
            {isEdit && camera ? (
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-7 shrink-0 text-xs'
                disabled={isRefreshingRevision}
                onClick={() => void handleRefreshRevision()}
              >
                <RefreshCw
                  className={cn(
                    'me-1 size-3',
                    isRefreshingRevision && 'animate-spin'
                  )}
                />
                {t('actions.refreshLatest')}
              </Button>
            ) : null}
          </div>
        ) : null}

        {probeError ? (
          <div
            className={cn(
              'flex items-start gap-2.5 rounded-lg border p-3 text-sm',
              STATUS_LEVEL_CLASS.destructive
            )}
          >
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{probeError}</div>
          </div>
        ) : null}

        <Form {...form}>
          <form
            id='camera-action-form'
            onSubmit={form.handleSubmit(onSubmit)}
            className='space-y-4'
          >
            {/* 设备名称 */}
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('form.name')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('form.namePlaceholder')}
                      autoComplete='off'
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {isGb28181 ? (
              <div
                className='rounded-md border border-border bg-muted/30 p-3 text-sm text-muted-foreground'
                role='note'
              >
                {t('form.gb28181Managed')}
              </div>
            ) : (
              <CamerasActionStreamFields control={form.control} />
            )}

            {/* 启用设备开关 */}
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border border-border p-3'>
                  <FormLabel className='text-sm font-medium'>
                    {t('form.enabled')}
                  </FormLabel>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
          </form>
        </Form>
      </div>

      <SheetFooter className='mt-0 shrink-0 gap-y-2 border-t bg-muted/20 px-6 py-3.5 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
          {isPending ? (
            <>
              <Loader2 className='size-3.5 animate-spin text-primary' />
              <span>
                {isGb28181
                  ? t('dialog.savingNotice')
                  : t('dialog.probingNotice')}
              </span>
            </>
          ) : null}
        </div>
        <div className='flex w-full items-center justify-end gap-2 sm:w-auto'>
          <SheetClose asChild>
            <Button variant='outline' disabled={isPending}>
              {t('actions.cancel')}
            </Button>
          </SheetClose>
          <Button
            type='submit'
            form='camera-action-form'
            disabled={isPending}
            className='min-w-24'
          >
            {isPending ? (
              <Loader2 className='me-1.5 size-4 animate-spin' />
            ) : null}
            {isPending
              ? isEdit
                ? t('actions.saving')
                : t('actions.probing')
              : isEdit
                ? t('actions.saveChanges')
                : t('actions.confirmAdd')}
          </Button>
        </div>
      </SheetFooter>
    </>
  )
}

export function CamerasActionDialog() {
  const { open, setOpen, currentRow, discoveredDevice, setDiscoveredDevice } =
    useCamerasContext()
  const [isPending, setIsPending] = useState(false)

  const isEdit = open === 'edit' && Boolean(currentRow)
  const isOpen = open === 'create' || isEdit

  const handleClose = () => {
    setDiscoveredDevice(null)
    setOpen(null)
  }

  return (
    <Sheet
      open={isOpen}
      onOpenChange={(val) => {
        // 探测/提交期间拒绝遮罩点击与 ESC 的关闭请求，避免异步中间态被丢弃
        if (!isPending && !val) {
          handleClose()
        }
      }}
    >
      <SheetContent
        side='right'
        className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
        {isOpen ? (
          <CamerasActionDialogBody
            key={
              isEdit && currentRow
                ? `edit-${currentRow.id}`
                : discoveredDevice
                  ? `create-${discoveredDevice.id || discoveredDevice.ip}`
                  : 'create'
            }
            isEdit={isEdit}
            camera={currentRow}
            onClose={handleClose}
            onPendingChange={setIsPending}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

// 语义别名：抽屉化后新代码可直接按 Drawer 命名引用，原导出名继续兼容既有调用点
export const CamerasActionDrawer = CamerasActionDialog
