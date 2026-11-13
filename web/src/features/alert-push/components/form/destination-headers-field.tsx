import { useMemo } from 'react'
import { useFieldArray, type Control } from 'react-hook-form'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormField,
  FormItem,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  RESERVED_HEADERS,
  type AlertDestinationFormValues,
} from '../../data/schema'

interface DestinationHeadersFieldProps {
  control: Control<AlertDestinationFormValues>
  isEdit?: boolean
  originalHeaderNames?: string[]
}

export function DestinationHeadersField({
  control,
  isEdit = false,
  originalHeaderNames = [],
}: DestinationHeadersFieldProps) {
  const { t } = useTranslation('alertPush')
  const { fields, append, remove } = useFieldArray({
    control,
    name: 'headers',
  })

  const retainedKeySet = useMemo(
    () => new Set(originalHeaderNames),
    [originalHeaderNames]
  )

  return (
    <div className='space-y-2.5 border-t border-border/50 pt-1'>
      <div className='flex items-center justify-between'>
        <Label className='text-xs font-medium text-foreground'>
          {t('fields.customHeaders')}
        </Label>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => append({ key: '', value: '' })}
          className='h-6.5 gap-1 px-2 text-[11px] shadow-2xs'
        >
          <Plus className='size-3' />
          <span>{t('fields.addHeader')}</span>
        </Button>
      </div>

      {fields.length === 0 ? (
        <div className='flex h-12 items-center justify-center rounded-md border border-dashed border-border/70 text-xs text-muted-foreground'>
          {t('fields.noHeaders')}
        </div>
      ) : (
        <div className='space-y-2'>
          {fields.map((fieldItem, index) => {
            const isRetainedKey =
              isEdit && retainedKeySet.has(fieldItem.key.trim())

            return (
              <div key={fieldItem.id} className='flex items-start gap-2'>
                <FormField
                  control={control}
                  name={`headers.${index}.key`}
                  render={({ field }) => {
                    const isReserved = RESERVED_HEADERS.includes(
                      field.value
                        .trim()
                        .toLowerCase() as (typeof RESERVED_HEADERS)[number]
                    )

                    return (
                      <FormItem className='flex-1 space-y-1'>
                        <FormControl>
                          <Input
                            {...field}
                            placeholder={t('fields.headerKeyPlaceholder')}
                            autoComplete='off'
                            className={cn(
                              'h-8 bg-background font-mono text-xs',
                              isReserved &&
                                'border-destructive focus-visible:ring-destructive/30'
                            )}
                          />
                        </FormControl>
                        <FormMessage className='text-[11px]' />
                      </FormItem>
                    )
                  }}
                />

                <FormField
                  control={control}
                  name={`headers.${index}.value`}
                  render={({ field }) => (
                    <FormItem className='flex-1 space-y-1'>
                      <FormControl>
                        <Input
                          {...field}
                          type='password'
                          autoComplete='off'
                          placeholder={
                            isRetainedKey
                              ? t('fields.secretRetained')
                              : t('fields.headerValuePlaceholder')
                          }
                          className='h-8 bg-background font-mono text-xs'
                        />
                      </FormControl>
                      <FormMessage className='text-[11px]' />
                    </FormItem>
                  )}
                />

                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  onClick={() => remove(index)}
                  className='size-8 shrink-0 text-muted-foreground hover:text-destructive'
                >
                  <Trash2 className='size-3.5' />
                  <span className='sr-only'>{t('actions.removeHeader')}</span>
                </Button>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
