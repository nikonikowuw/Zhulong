import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { Button } from '@/components/ui/button'
import {
  Dialog,
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
import {
  createMapUploadFormSchema,
  type MapUploadFormValues,
} from '../../data/schema'
import { useUploadMap } from '../../hooks/use-spatial-maps'

interface MapUploadDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: (mapId: string) => void
}

const ACCEPTED_MAP_TYPES = 'image/png,image/jpeg,image/webp,image/svg+xml'

/**
 * 初始值：`file` 是必填项却没有默认值，只能以类型断言表达「尚未选择」。
 */
function emptyUploadValues(): MapUploadFormValues {
  return { name: '', file: undefined as unknown as File }
}

export function MapUploadDialog({
  open,
  onOpenChange,
  onSuccess,
}: MapUploadDialogProps) {
  const { t } = useTranslation('spatial')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadMutation = useUploadMap()

  const schema = useMemo(() => createMapUploadFormSchema(t), [t])
  const form = useForm<MapUploadFormValues>({
    resolver: zodResolver(schema),
    defaultValues: emptyUploadValues(),
  })

  const [previewUrl, setPreviewUrl] = useState<string | null>(null)

  // 当 previewUrl 变更或组件卸载时，自动清理上一份 object URL
  useEffect(() => {
    return () => {
      if (previewUrl) {
        URL.revokeObjectURL(previewUrl)
      }
    }
  }, [previewUrl])

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      form.reset(emptyUploadValues())
      setPreviewUrl(null)
    }
    onOpenChange(nextOpen)
  }

  const handleFileChange = (picked: File | null) => {
    if (!picked) return
    form.setValue('file', picked, { shouldValidate: true })
    if (form.getValues('name').trim() === '') {
      // 默认用无扩展名文件名作为底图名称，用户仍可覆盖。
      form.setValue('name', picked.name.replace(/\.[^/.]+$/, ''), {
        shouldValidate: true,
      })
    }

    // 副作用在用户事件处理器中创建，生命周期回收由上面的 effect cleanup 负责
    setPreviewUrl(URL.createObjectURL(picked))
  }

  const onSubmit = async (values: MapUploadFormValues) => {
    try {
      const created = await uploadMutation.mutateAsync({
        name: values.name.trim(),
        file: values.file,
      })
      toast.success(t('map.uploadSuccess'))
      form.reset(emptyUploadValues())
      setPreviewUrl(null)
      onOpenChange(false)
      onSuccess(created.id)
    } catch (err: unknown) {
      handleFormError(form, err, {
        fieldMap: { name: 'name' },
        onUnhandled: (message) => toast.error(message || t('map.uploadFailed')),
      })
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)}>
            <DialogHeader>
              <DialogTitle>{t('map.uploadTitle')}</DialogTitle>
              <DialogDescription>
                {t('map.uploadDescription')}
              </DialogDescription>
            </DialogHeader>

            <div className='grid gap-4 py-4'>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('map.mapName')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('map.mapNamePlaceholder')}
                        disabled={uploadMutation.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='file'
                render={() => (
                  <FormItem>
                    <FormLabel>{t('map.file')}</FormLabel>
                    <div
                      className='flex cursor-pointer flex-col items-center justify-center rounded-lg border-2 border-dashed border-border p-6 transition-colors hover:bg-muted/50'
                      onClick={() => fileInputRef.current?.click()}
                      onDragOver={(e) => e.preventDefault()}
                      onDrop={(e) => {
                        e.preventDefault()
                        handleFileChange(e.dataTransfer.files?.[0] ?? null)
                      }}
                    >
                      <input
                        ref={fileInputRef}
                        type='file'
                        accept={ACCEPTED_MAP_TYPES}
                        className='hidden'
                        onChange={(e) =>
                          handleFileChange(e.target.files?.[0] ?? null)
                        }
                      />
                      {previewUrl ? (
                        <div className='relative flex h-36 w-full items-center justify-center overflow-hidden rounded'>
                          <img
                            src={previewUrl}
                            alt={t('map.file')}
                            className='max-h-full max-w-full object-contain'
                          />
                        </div>
                      ) : (
                        <div className='flex flex-col items-center text-center text-muted-foreground'>
                          <Upload className='mb-2 h-8 w-8' />
                          <span className='text-sm'>
                            {t('map.dragDropTip')}
                          </span>
                        </div>
                      )}
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => handleOpenChange(false)}
                disabled={uploadMutation.isPending}
              >
                {t('common:actions.cancel')}
              </Button>
              <Button type='submit' disabled={uploadMutation.isPending}>
                {uploadMutation.isPending && (
                  <Loader2 className='me-2 h-4 w-4 animate-spin' />
                )}
                {t('map.uploadMap')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
