import { type Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  INLINE_MAX_KB_MAX,
  INLINE_MAX_KB_MIN,
  type AlertDestinationFormValues,
} from '../../data/schema'

/**
 * MQTT base64 模式的内联图片预算（KB）。由父级按当前图片模式决定是否渲染：
 * 本组件不自行订阅 imageMode，避免与协议切换同一轮挂载时读到旧值。
 */
export function InlineBudgetField({
  control,
}: {
  control: Control<AlertDestinationFormValues>
}) {
  const { t } = useTranslation('alertPush')

  return (
    <FormField
      control={control}
      name='inlineMaxKb'
      render={({ field }) => (
        <FormItem className='space-y-1.5'>
          <FormLabel className='text-xs font-medium'>
            {t('fields.inlineMaxKb')} ({t('fields.inlineMaxKbUnit')})
          </FormLabel>
          <FormControl>
            <Input
              type='number'
              min={INLINE_MAX_KB_MIN}
              max={INLINE_MAX_KB_MAX}
              value={Number.isNaN(field.value) ? '' : field.value}
              onChange={(e) => {
                const raw = e.target.value
                field.onChange(raw === '' ? Number.NaN : Number(raw))
              }}
              className='h-8 bg-background font-mono text-xs'
            />
          </FormControl>
          <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
            {t('fields.inlineMaxKbHint')}
          </FormDescription>
          <FormMessage className='text-xs' />
        </FormItem>
      )}
    />
  )
}
