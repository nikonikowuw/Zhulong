import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'
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
import { useCreatePerson, useUpdatePerson } from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

const createPersonFormSchema = (t: (key: string) => string) =>
  z.object({
    name: z.string().trim().min(1, t('validation.nameRequired')).max(64),
    personId: z.string().trim().max(64).optional(),
  })

type PersonFormValues = z.infer<ReturnType<typeof createPersonFormSchema>>

export function PersonsActionDialog() {
  const { t } = useTranslation('persons')
  const { open, setOpen, currentRow } = usePersonsContext()

  const isEdit = open === 'edit' && Boolean(currentRow)
  const isOpen = open === 'create' || open === 'edit'

  const createMutation = useCreatePerson()
  const updateMutation = useUpdatePerson()
  const isPending = createMutation.isPending || updateMutation.isPending

  const formSchema = useMemo(() => createPersonFormSchema(t), [t])

  const form = useForm<PersonFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: '',
      personId: '',
    },
  })

  useEffect(() => {
    if (isOpen) {
      if (isEdit && currentRow) {
        form.reset({
          name: currentRow.name,
          personId: currentRow.personId,
        })
      } else {
        form.reset({
          name: '',
          personId: '',
        })
      }
    }
  }, [isOpen, isEdit, currentRow, form])

  const onSubmit = async (values: PersonFormValues) => {
    try {
      if (isEdit && currentRow) {
        await updateMutation.mutateAsync({
          id: currentRow.id,
          data: { name: values.name },
        })
        toast.success(t('notifications.updateSuccess', { name: values.name }))
      } else {
        await createMutation.mutateAsync({
          name: values.name,
          personId: values.personId ? values.personId : undefined,
        })
        toast.success(t('notifications.createSuccess', { name: values.name }))
      }
      setOpen(null)
      form.reset()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(msg)
    }
  }

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(v) => {
        if (!v) {
          setOpen(null)
          form.reset()
        }
      }}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t('dialogs.editTitle') : t('dialogs.createTitle')}
          </DialogTitle>
          <DialogDescription>
            {isEdit ? t('dialogs.editDesc') : t('dialogs.createDesc')}
          </DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(onSubmit)}
            className='space-y-4 py-2'
          >
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('fields.name')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('fields.namePlaceholder')}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='personId'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('fields.personId')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('fields.personIdPlaceholder')}
                      disabled={isEdit}
                      {...field}
                    />
                  </FormControl>
                  {isEdit ? (
                    <FormDescription className='text-xs text-muted-foreground'>
                      {t('fields.personIdDisabledTip')}
                    </FormDescription>
                  ) : null}
                  <FormMessage />
                </FormItem>
              )}
            />

            <DialogFooter className='pt-2'>
              <DialogClose asChild>
                <Button variant='outline' type='button' disabled={isPending}>
                  {t('actions.cancel')}
                </Button>
              </DialogClose>
              <Button type='submit' disabled={isPending}>
                {isPending && (
                  <Loader2 className='me-2 h-4 w-4 animate-spin' />
                )}
                {isPending ? t('actions.saving') : t('actions.confirm')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
