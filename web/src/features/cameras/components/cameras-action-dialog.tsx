import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { AlertCircle, Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
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
            name: '',
            enabled: true,
            mainRtspUrl: '',
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
      <DialogHeader className='text-start'>
        <DialogTitle>
          {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
        </DialogTitle>
        <DialogDescription>
          {isGb28181
            ? t('dialog.gb28181EditDesc')
            : isEdit
              ? t('dialog.editDesc')
              : t('dialog.createDesc')}
        </DialogDescription>
      </DialogHeader>

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

      <div className='max-h-[70vh] overflow-y-auto py-1 pe-2'>
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

      <DialogFooter className='gap-y-2 pt-2 sm:items-center sm:justify-between'>
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
        <div className='flex items-center justify-end gap-2'>
          <DialogClose asChild>
            <Button variant='outline' disabled={isPending}>
              {t('actions.cancel')}
            </Button>
          </DialogClose>
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
      </DialogFooter>
    </>
  )
}

export function CamerasActionDialog() {
  const { open, setOpen, currentRow } = useCamerasContext()
  const [isPending, setIsPending] = useState(false)

  const isEdit = open === 'edit' && Boolean(currentRow)
  const isOpen = open === 'create' || isEdit

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(val) => {
        if (!isPending && !val) {
          setOpen(null)
        }
      }}
    >
      <DialogContent className='w-full sm:max-w-lg'>
        {isOpen ? (
          <CamerasActionDialogBody
            key={isEdit && currentRow ? `edit-${currentRow.id}` : 'create'}
            isEdit={isEdit}
            camera={currentRow}
            onClose={() => setOpen(null)}
            onPendingChange={setIsPending}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
