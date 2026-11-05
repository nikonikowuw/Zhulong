import { useState } from 'react'
import { useWatch, type Control } from 'react-hook-form'
import { ChevronDown, Globe, Sliders } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  FormControl,
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
import type { AlertDestinationFormValues } from '../../data/schema'
import type { AuthType } from '../../types'
import { DestinationHeadersField } from './destination-headers-field'

interface WebhookConfigSectionProps {
  control: Control<AlertDestinationFormValues>
  isEdit: boolean
  secretSet?: boolean
  initialAdvancedOpen?: boolean
}

/**
 * 局部摘要徽标：将 timeoutSeconds 与 headers 的监听限制在叶子节点，
 * 避免输入超时时间或操作标头时引发上层表单级重渲染
 */
function WebhookAdvancedSummary({
  control,
}: {
  control: Control<AlertDestinationFormValues>
}) {
  const { t } = useTranslation('alertPush')
  const timeoutSeconds = useWatch({
    control,
    name: 'timeoutSeconds',
  })
  const headers = useWatch({
    control,
    name: 'headers',
  })

  return (
    <span className='text-[11px] font-normal text-muted-foreground'>
      (
      {t('advanced.webhookSummary', {
        timeout: timeoutSeconds,
        count: headers?.length || 0,
      })}
      )
    </span>
  )
}

export function WebhookConfigSection({
  control,
  isEdit,
  secretSet,
  initialAdvancedOpen = false,
}: WebhookConfigSectionProps) {
  const { t } = useTranslation('alertPush')
  const [advancedOpen, setAdvancedOpen] = useState(initialAdvancedOpen)

  const authType = useWatch({
    control,
    name: 'authType',
  })

  return (
    <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
      <div className='flex items-center gap-2 border-b border-border/50 pb-2 text-xs font-semibold text-foreground'>
        <Globe className='size-3.5 text-primary' />
        <span>{t('sections.webhookEndpoint')}</span>
      </div>

      {/* Webhook URL 直接在中心展示 */}
      <FormField
        control={control}
        name='url'
        render={({ field }) => (
          <FormItem>
            <FormLabel className='text-xs font-medium'>
              {t('fields.webhookUrl')}
            </FormLabel>
            <FormControl>
              <Input
                {...field}
                placeholder={t('fields.webhookUrlPlaceholder')}
                className='font-mono text-xs'
                autoComplete='off'
              />
            </FormControl>
            <FormMessage className='text-xs' />
          </FormItem>
        )}
      />

      {/* 鉴权模式与凭据 */}
      <div className='space-y-3 pt-1'>
        <FormField
          control={control}
          name='authType'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-xs font-medium'>
                {t('fields.authType')}
              </FormLabel>
              <Select
                value={field.value}
                onValueChange={(val) => field.onChange(val as AuthType)}
              >
                <FormControl>
                  <SelectTrigger className='h-8.5 bg-background text-xs'>
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectItem value='none'>{t('authTypes.none')}</SelectItem>
                  <SelectItem value='bearer'>
                    {t('authTypes.bearer')}
                  </SelectItem>
                  <SelectItem value='basic'>{t('authTypes.basic')}</SelectItem>
                </SelectContent>
              </Select>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        {authType === 'bearer' ? (
          <FormField
            control={control}
            name='token'
            render={({ field }) => (
              <FormItem className='rounded-lg border border-border/60 bg-muted/20 p-3'>
                <FormLabel className='text-xs font-medium'>
                  {t('fields.bearerToken')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='password'
                    placeholder={
                      isEdit && secretSet
                        ? t('fields.secretRetained')
                        : t('fields.bearerTokenPlaceholder')
                    }
                    className='bg-background font-mono text-xs'
                    autoComplete='off'
                  />
                </FormControl>
                <FormMessage className='text-xs' />
              </FormItem>
            )}
          />
        ) : null}

        {authType === 'basic' ? (
          <div className='grid grid-cols-1 gap-3 rounded-lg border border-border/60 bg-muted/20 p-3 sm:grid-cols-2'>
            <FormField
              control={control}
              name='basicUser'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.basicUsername')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      placeholder={
                        isEdit && secretSet
                          ? t('fields.secretRetained')
                          : t('fields.basicUsernamePlaceholder')
                      }
                      className='bg-background font-mono text-xs'
                      autoComplete='off'
                    />
                  </FormControl>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />

            <FormField
              control={control}
              name='basicPass'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.basicPassword')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='password'
                      placeholder={
                        isEdit && secretSet
                          ? t('fields.secretRetained')
                          : t('fields.basicPasswordPlaceholder')
                      }
                      className='bg-background font-mono text-xs'
                      autoComplete='off'
                    />
                  </FormControl>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />
          </div>
        ) : null}
      </div>

      {/* 可折叠的高级传输与标头 */}
      <Collapsible
        open={advancedOpen}
        onOpenChange={setAdvancedOpen}
        className='space-y-3 rounded-lg border border-border/60 bg-muted/20 p-3 transition-colors'
      >
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
            <Sliders className='size-3.5 text-muted-foreground' />
            <span>{t('advanced.webhookTitle')}</span>
            <WebhookAdvancedSummary control={control} />
          </div>
          <CollapsibleTrigger asChild>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='h-7 w-7 p-0'
            >
              <ChevronDown
                className={`size-4 transition-transform duration-200 ${
                  advancedOpen ? 'rotate-180' : ''
                }`}
              />
              <span className='sr-only'>Toggle Advanced Webhook</span>
            </Button>
          </CollapsibleTrigger>
        </div>

        <CollapsibleContent className='space-y-3.5 pt-1'>
          {/* 超时时间 */}
          <FormField
            control={control}
            name='timeoutSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('fields.timeout')} ({t('fields.timeoutUnit')})
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={30}
                    value={field.value}
                    onChange={(e) => field.onChange(Number(e.target.value))}
                    className='bg-background font-mono text-xs'
                  />
                </FormControl>
                <FormMessage className='text-xs' />
              </FormItem>
            )}
          />

          {/* 自定义标头 */}
          <DestinationHeadersField control={control} />
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}
