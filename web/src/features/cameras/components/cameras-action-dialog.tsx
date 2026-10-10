import { useEffect, useState } from 'react'
import { AxiosError } from 'axios'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { cameraFormSchema, type CameraFormValues } from '../data/schema'
import { useCreateCamera, useUpdateCamera } from '../hooks/use-cameras'
import { useCamerasContext } from './cameras-provider'

export function CamerasActionDialog() {
  const { t } = useTranslation('cameras')
  const { open, setOpen, currentRow } = useCamerasContext()
  const [probeError, setProbeError] = useState<string | null>(null)

  const isEdit = open === 'edit' && Boolean(currentRow)
  const isOpen = open === 'create' || open === 'edit'

  const createMutation = useCreateCamera()
  const updateMutation = useUpdateCamera()
  const isPending = createMutation.isPending || updateMutation.isPending

  const form = useForm<CameraFormValues>({
    resolver: zodResolver(cameraFormSchema),
    defaultValues: {
      name: '',
      enabled: true,
      mainRtspUrl: '',
      mainTransport: 'tcp',
      hasSubStream: false,
      subRtspUrl: '',
      subTransport: 'tcp',
    },
  })

  // 当打开或 currentRow 变化时同步回填
  useEffect(() => {
    if (isOpen) {
      setProbeError(null)
      if (isEdit && currentRow) {
        const main = currentRow.streams?.find((s) => s.role === 'main')
        const sub = currentRow.streams?.find((s) => s.role === 'sub')
        form.reset({
          name: currentRow.name,
          enabled: currentRow.enabled,
          mainRtspUrl: main?.rtspUrl || '',
          mainTransport: (main?.transport as 'tcp' | 'udp') || 'tcp',
          hasSubStream: Boolean(sub),
          subRtspUrl: sub?.rtspUrl || '',
          subTransport: (sub?.transport as 'tcp' | 'udp') || 'tcp',
        })
      } else {
        form.reset({
          name: '',
          enabled: true,
          mainRtspUrl: '',
          mainTransport: 'tcp',
          hasSubStream: false,
          subRtspUrl: '',
          subTransport: 'tcp',
        })
      }
    }
  }, [isOpen, isEdit, currentRow, form])

  const onSubmit = async (values: CameraFormValues) => {
    if (isPending) return
    setProbeError(null)

    const streamPayload = {
      name: values.name,
      enabled: values.enabled,
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
        : undefined,
    }

    try {
      if (isEdit && currentRow) {
        await updateMutation.mutateAsync({
          id: currentRow.id,
          data: {
            revision: currentRow.revision,
            ...streamPayload,
          },
        })
        toast.success(t('toast.updateSuccess'))
      } else {
        await createMutation.mutateAsync(streamPayload)
        toast.success(t('toast.createSuccess'))
      }
      setOpen(null)
    } catch (err: unknown) {
      if (err instanceof AxiosError) {
        if (err.response?.status === 409) {
          toast.error(t('toast.casConflict'))
          setOpen(null)
          return
        }
        const message =
          err.response?.data?.message || err.message || 'Probe or save failed'
        setProbeError(message)
        toast.error(t('toast.probeFailed', { reason: message }))
      } else {
        const errorText = String(err)
        setProbeError(errorText)
        toast.error(errorText)
      }
    }
  }

  // eslint-disable-next-line react-hooks/incompatible-library
  const hasSub = form.watch('hasSubStream')

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
        <DialogHeader className='text-start'>
          <DialogTitle>
            {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
          </DialogTitle>
          <DialogDescription>
            {isEdit ? t('dialog.editDesc') : t('dialog.createDesc')}
          </DialogDescription>
        </DialogHeader>

        {probeError && (
          <div className='flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{probeError}</div>
          </div>
        )}

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

              {/* 主流 RTSP URL */}
              <FormField
                control={form.control}
                name='mainRtspUrl'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('form.mainRtsp')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('form.mainRtspPlaceholder')}
                        className='font-mono'
                        autoComplete='off'
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* 主流传输协议 */}
              <FormField
                control={form.control}
                name='mainTransport'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('form.mainTransport')}</FormLabel>
                    <Select
                      onValueChange={field.onChange}
                      defaultValue={field.value}
                    >
                      <FormControl>
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value='tcp'>
                          {t('form.transportTcp')}
                        </SelectItem>
                        <SelectItem value='udp'>
                          {t('form.transportUdp')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* 子流开关与配置 */}
              <FormField
                control={form.control}
                name='hasSubStream'
                render={({ field }) => (
                  <FormItem className='flex flex-row items-center justify-between rounded-lg border border-border p-3'>
                    <div className='space-y-0.5'>
                      <FormLabel className='text-sm font-medium'>
                        {t('table.columns.subStream')}
                      </FormLabel>
                      <FormDescription className='text-xs'>
                        {t('form.hasSubStream')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />

              {hasSub && (
                <div className='space-y-4 rounded-lg border border-border/80 bg-muted/30 p-3.5'>
                  <FormField
                    control={form.control}
                    name='subRtspUrl'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('form.subRtsp')}</FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t('form.subRtspPlaceholder')}
                            className='font-mono'
                            autoComplete='off'
                            {...field}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='subTransport'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('form.subTransport')}</FormLabel>
                        <Select
                          onValueChange={field.onChange}
                          defaultValue={field.value}
                        >
                          <FormControl>
                            <SelectTrigger className='w-full'>
                              <SelectValue />
                            </SelectTrigger>
                          </FormControl>
                          <SelectContent>
                            <SelectItem value='tcp'>
                              {t('form.transportTcp')}
                            </SelectItem>
                            <SelectItem value='udp'>
                              {t('form.transportUdp')}
                            </SelectItem>
                          </SelectContent>
                        </Select>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
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
            {isPending && (
              <>
                <Loader2 className='size-3.5 animate-spin text-primary' />
                <span>{t('dialog.probingNotice')}</span>
              </>
            )}
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
              {isPending && <Loader2 className='me-1.5 size-4 animate-spin' />}
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
      </DialogContent>
    </Dialog>
  )
}
