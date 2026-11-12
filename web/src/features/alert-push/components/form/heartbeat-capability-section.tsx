import { useWatch, type Control } from 'react-hook-form'
import { HeartPulse } from 'lucide-react'
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
import { Switch } from '@/components/ui/switch'
import type { AlertDestinationFormValues } from '../../data/schema'

interface HeartbeatCapabilitySectionProps {
  control: Control<AlertDestinationFormValues>
}

export function HeartbeatCapabilitySection({
  control,
}: HeartbeatCapabilitySectionProps) {
  const { t } = useTranslation('alertPush')

  const heartbeatEnabled = useWatch({
    control,
    name: 'heartbeatEnabled',
  })

  const channelType = useWatch({
    control,
    name: 'channelType',
  })

  return (
    <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs transition-colors'>
      {/* 能力头：设备存活上报开关 */}
      <FormField
        control={control}
        name='heartbeatEnabled'
        render={({ field }) => (
          <FormItem className='space-y-1.5'>
            <div className='flex items-start justify-between gap-3'>
              <div className='flex items-start gap-2.5'>
                <div className='mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-background text-primary shadow-2xs'>
                  <HeartPulse className='size-3.5' />
                </div>
                <div className='space-y-0.5'>
                  <FormLabel className='cursor-pointer text-xs font-semibold text-foreground'>
                    {t('capabilities.heartbeatTitle')}
                  </FormLabel>
                  <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
                    {t('capabilities.heartbeatDesc')}
                  </FormDescription>
                </div>
              </div>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  aria-label={t('capabilities.heartbeatTitle')}
                />
              </FormControl>
            </div>
            <FormMessage className='text-xs' />
          </FormItem>
        )}
      />

      {heartbeatEnabled ? (
        <div className='space-y-3.5 border-t border-border/50 pt-3.5'>
          {/* 上报间隔 */}
          <FormField
            control={control}
            name='heartbeatIntervalSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('fields.heartbeatInterval')} (
                  {t('fields.heartbeatIntervalUnit')})
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={15}
                    max={3600}
                    value={field.value}
                    onChange={(e) => field.onChange(Number(e.target.value))}
                    className='bg-background font-mono text-xs'
                  />
                </FormControl>
                <FormDescription className='text-[11px] text-muted-foreground'>
                  {t('fields.heartbeatIntervalHint')}
                </FormDescription>
                <FormMessage className='text-xs' />
              </FormItem>
            )}
          />

          {channelType === 'webhook' ? (
            <FormField
              control={control}
              name='heartbeatUrl'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.heartbeatUrl')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      placeholder={t('fields.heartbeatUrlPlaceholder')}
                      className='bg-background font-mono text-xs'
                      autoComplete='off'
                    />
                  </FormControl>
                  <FormDescription className='text-[11px] text-muted-foreground'>
                    {t('fields.heartbeatUrlHint')}
                  </FormDescription>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />
          ) : (
            <div className='space-y-3'>
              <FormField
                control={control}
                name='heartbeatTopic'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className='text-xs font-medium'>
                      {t('fields.heartbeatTopic')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('fields.heartbeatTopicPlaceholder')}
                        className='bg-background font-mono text-xs'
                        autoComplete='off'
                      />
                    </FormControl>
                    <FormDescription className='text-[11px] text-muted-foreground'>
                      {t('fields.heartbeatTopicHint')}
                    </FormDescription>
                    <FormMessage className='text-xs' />
                  </FormItem>
                )}
              />

              <FormField
                control={control}
                name='lwtEnabled'
                render={({ field }) => (
                  <FormItem className='flex flex-row items-center justify-between rounded-lg border border-border/60 bg-muted/20 p-3'>
                    <div className='space-y-0.5 pr-3'>
                      <FormLabel className='cursor-pointer text-xs font-medium'>
                        {t('fields.lwtEnabled')}
                      </FormLabel>
                      <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
                        {t('fields.lwtEnabledDesc')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        aria-label={t('fields.lwtEnabled')}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
            </div>
          )}
        </div>
      ) : null}
    </div>
  )
}
