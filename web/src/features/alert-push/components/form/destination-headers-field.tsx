import { useFieldArray, type Control } from 'react-hook-form'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  RESERVED_HEADERS,
  type AlertDestinationFormValues,
} from '../../data/schema'

interface DestinationHeadersFieldProps {
  control: Control<AlertDestinationFormValues>
}

export function DestinationHeadersField({
  control,
}: DestinationHeadersFieldProps) {
  const { t } = useTranslation('alertPush')
  const { fields, append, remove, update } = useFieldArray({
    control,
    name: 'headers',
  })

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
            const isReserved = RESERVED_HEADERS.includes(
              fieldItem.key
                .trim()
                .toLowerCase() as (typeof RESERVED_HEADERS)[number]
            )

            return (
              <div key={fieldItem.id} className='space-y-1'>
                <div className='flex items-center gap-2'>
                  <Input
                    placeholder={t('fields.headerKeyPlaceholder')}
                    value={fieldItem.key}
                    onChange={(e) =>
                      update(index, {
                        ...fieldItem,
                        key: e.target.value,
                      })
                    }
                    className={cn(
                      'h-8 bg-background font-mono text-xs',
                      isReserved &&
                        'border-destructive focus-visible:ring-destructive/30'
                    )}
                  />
                  <Input
                    placeholder={t('fields.headerValuePlaceholder')}
                    value={fieldItem.value}
                    type='password'
                    onChange={(e) =>
                      update(index, {
                        ...fieldItem,
                        value: e.target.value,
                      })
                    }
                    className='h-8 bg-background font-mono text-xs'
                  />
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    onClick={() => remove(index)}
                    className='size-8 shrink-0 text-muted-foreground hover:text-destructive'
                  >
                    <Trash2 className='size-3.5' />
                    <span className='sr-only'>Remove Header</span>
                  </Button>
                </div>
                {isReserved ? (
                  <p className='text-[11px] text-destructive'>
                    {t('fields.reservedHeaderWarning')}
                  </p>
                ) : null}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
