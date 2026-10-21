import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { z } from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { ImagePlus, Loader2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
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
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  useAddFace,
  useCreatePerson,
  useUpdatePerson,
} from '../hooks/use-persons'
import { usePersonsContext } from './persons-provider'

const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ALLOWED_TYPES = ['image/jpeg', 'image/png', 'image/webp']

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
  const isPersonIdBound = Boolean(
    isEdit && currentRow?.personId && currentRow.personId.trim()
  )
  const isOpen = open === 'create' || open === 'edit'

  const createMutation = useCreatePerson()
  const updateMutation = useUpdatePerson()
  const addFaceMutation = useAddFace({ suppressGlobalToast: true })
  const isPending =
    createMutation.isPending ||
    updateMutation.isPending ||
    addFaceMutation.isPending

  const [initialFaceFile, setInitialFaceFile] = useState<File | null>(null)
  const [initialFacePreview, setInitialFacePreview] = useState<string | null>(
    null
  )
  const [isDragOver, setIsDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const clearFaceFile = useCallback(() => {
    setInitialFaceFile(null)
    setInitialFacePreview((prev) => {
      if (prev) URL.revokeObjectURL(prev)
      return null
    })
  }, [])

  useEffect(() => {
    return () => {
      if (initialFacePreview) {
        URL.revokeObjectURL(initialFacePreview)
      }
    }
  }, [initialFacePreview])

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
          personId: currentRow.personId || '',
        })
      } else {
        form.reset({
          name: '',
          personId: '',
        })
      }
    }
  }, [isOpen, isEdit, currentRow, form])

  const handleFaceSelect = (file: File) => {
    if (!ALLOWED_TYPES.includes(file.type)) {
      toast.error(t('validation.invalidFileType'))
      return
    }
    if (file.size > MAX_FILE_SIZE) {
      toast.error(t('validation.fileTooLarge'))
      return
    }
    setInitialFaceFile(file)
    setInitialFacePreview((prev) => {
      if (prev) URL.revokeObjectURL(prev)
      return URL.createObjectURL(file)
    })
  }

  const onSubmit = async (values: PersonFormValues) => {
    try {
      if (isEdit && currentRow) {
        const updatePayload: { name: string; personId?: string } = {
          name: values.name,
        }
        if (!isPersonIdBound && values.personId && values.personId.trim()) {
          updatePayload.personId = values.personId.trim()
        }
        await updateMutation.mutateAsync({
          id: currentRow.id,
          data: updatePayload,
        })
        toast.success(t('notifications.updateSuccess', { name: values.name }))
      } else {
        const created = await createMutation.mutateAsync({
          name: values.name,
          personId: values.personId ? values.personId : undefined,
        })
        if (initialFaceFile) {
          try {
            await addFaceMutation.mutateAsync({
              personId: created.id,
              data: initialFaceFile,
            })
            toast.success(
              t('notifications.createSuccessWithFace', { name: values.name })
            )
          } catch (faceErr: unknown) {
            const errorMsg =
              faceErr instanceof Error ? faceErr.message : String(faceErr)
            toast.warning(
              t('notifications.createSuccessFaceFailed', {
                name: values.name,
                error: errorMsg,
              })
            )
          }
        } else {
          toast.success(t('notifications.createSuccess', { name: values.name }))
        }
      }
      setOpen(null)
      clearFaceFile()
      form.reset()
    } catch (err: unknown) {
      handleFormError(form, err)
    }
  }

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(v) => {
        if (!v) {
          setOpen(null)
          clearFaceFile()
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
                      disabled={isPersonIdBound}
                      {...field}
                    />
                  </FormControl>
                  {isPersonIdBound ? (
                    <FormDescription className='text-xs text-muted-foreground'>
                      {t('fields.personIdDisabledTip')}
                    </FormDescription>
                  ) : null}
                  <FormMessage />
                </FormItem>
              )}
            />

            {!isEdit && (
              <div className='space-y-1.5'>
                <span className='text-sm leading-none font-medium text-foreground'>
                  {t('fields.initialFace')}
                </span>
                <input
                  type='file'
                  ref={fileInputRef}
                  accept='image/jpeg,image/png,image/webp'
                  onChange={(e) => {
                    const f = e.target.files?.[0]
                    if (f) handleFaceSelect(f)
                  }}
                  className='hidden'
                />
                {!initialFaceFile ? (
                  <div
                    onClick={() => fileInputRef.current?.click()}
                    onDragOver={(e) => {
                      e.preventDefault()
                      setIsDragOver(true)
                    }}
                    onDragLeave={() => setIsDragOver(false)}
                    onDrop={(e) => {
                      e.preventDefault()
                      setIsDragOver(false)
                      const f = e.dataTransfer.files?.[0]
                      if (f) handleFaceSelect(f)
                    }}
                    className={cn(
                      'flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-lg border-2 border-dashed p-4 text-center transition-colors',
                      isDragOver
                        ? 'border-primary bg-primary/10'
                        : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/40'
                    )}
                  >
                    <ImagePlus className='h-6 w-6 text-muted-foreground' />
                    <span className='text-xs font-medium text-foreground'>
                      {t('fields.dragDropTip')}
                    </span>
                    <span className='text-[11px] text-muted-foreground/80'>
                      {t('fields.initialFaceTip')}
                    </span>
                  </div>
                ) : (
                  <div className='relative flex items-center gap-3 rounded-lg border bg-muted/30 p-2'>
                    {initialFacePreview && (
                      <img
                        src={initialFacePreview}
                        alt='preview'
                        className='h-14 w-14 shrink-0 rounded-md border object-cover'
                      />
                    )}
                    <div className='min-w-0 flex-1 text-xs'>
                      <p className='truncate font-medium text-foreground'>
                        {initialFaceFile.name}
                      </p>
                      <p className='text-muted-foreground'>
                        {(initialFaceFile.size / 1024).toFixed(1)} KB
                      </p>
                    </div>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => {
                        clearFaceFile()
                        if (fileInputRef.current) {
                          fileInputRef.current.value = ''
                        }
                      }}
                      className='hover:text-destructive-foreground h-7 w-7 rounded-full p-0 text-muted-foreground hover:bg-destructive'
                      aria-label={t('fields.removeSelected')}
                    >
                      <X className='h-4 w-4' />
                    </Button>
                  </div>
                )}
              </div>
            )}

            <DialogFooter className='pt-2'>
              <DialogClose asChild>
                <Button variant='outline' type='button' disabled={isPending}>
                  {t('actions.cancel')}
                </Button>
              </DialogClose>
              <Button type='submit' disabled={isPending}>
                {isPending && <Loader2 className='me-2 h-4 w-4 animate-spin' />}
                {isPending ? t('actions.saving') : t('actions.confirm')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
