import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
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
  createMapRenameFormSchema,
  type MapRenameFormValues,
} from '../../data/schema'
import { useUpdateMapName } from '../../hooks/use-spatial-maps'

interface MapRenameDialogProps {
  mapId: string
  currentName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MapRenameDialog({
  mapId,
  currentName,
  open,
  onOpenChange,
}: MapRenameDialogProps) {
  const { t } = useTranslation('spatial')
  const renameMutation = useUpdateMapName()

  const schema = useMemo(() => createMapRenameFormSchema(t), [t])

  const form = useForm<MapRenameFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: currentName },
  })

  useEffect(() => {
    if (open) {
      form.reset({ name: currentName })
    }
  }, [open, currentName, form])

  const onSubmit = async (values: MapRenameFormValues) => {
    try {
      await renameMutation.mutateAsync({ id: mapId, name: values.name.trim() })
      toast.success(t('map.renameSuccess'))
      onOpenChange(false)
    } catch (err: unknown) {
      handleFormError(form, err, {
        fieldMap: { name: 'name' },
        onUnhandled: (message) => toast.error(message || t('map.renameFailed')),
      })
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)}>
            <DialogHeader>
              <DialogTitle>{t('map.renameTitle')}</DialogTitle>
              <DialogDescription>
                {t('map.renameDescription')}
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
                        disabled={renameMutation.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
                disabled={renameMutation.isPending}
              >
                {t('common:actions.cancel')}
              </Button>
              <Button type='submit' disabled={renameMutation.isPending}>
                {renameMutation.isPending && (
                  <Loader2 className='me-2 h-4 w-4 animate-spin' />
                )}
                {t('map.rename')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
