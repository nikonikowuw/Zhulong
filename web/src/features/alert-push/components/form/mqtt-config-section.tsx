import { useState } from 'react'
import { useWatch, type Control } from 'react-hook-form'
import { ChevronDown, Radio, Sliders } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type { AlertDestinationFormValues } from '../../data/schema'

interface MqttConfigSectionProps {
  control: Control<AlertDestinationFormValues>
  isEdit: boolean
  secretSet?: boolean
  initialAdvancedOpen?: boolean
}

/**
 * 局部摘要徽标：将 qos, keepAliveSeconds, clientId 的监听限制在叶子节点，
 * 避免用户在这些输入框输入时引发上层表单级重渲染
 */
function MqttAdvancedSummary({
  control,
}: {
  control: Control<AlertDestinationFormValues>
}) {
  const { t } = useTranslation('alertPush')
  const qos = useWatch({ control, name: 'qos' })
  const keepAliveSeconds = useWatch({ control, name: 'keepAliveSeconds' })
  const clientId = useWatch({ control, name: 'clientId' })

  return (
    <span className='text-[11px] font-normal text-muted-foreground'>
      (
      {t('advanced.mqttSummary', {
        qos,
        keepAlive: keepAliveSeconds,
        clientId: clientId || t('fields.mqttClientIdAuto'),
      })}
      )
    </span>
  )
}

export function MqttConfigSection({
  control,
  isEdit,
  secretSet,
  initialAdvancedOpen = false,
}: MqttConfigSectionProps) {
  const { t } = useTranslation('alertPush')
  const [advancedOpen, setAdvancedOpen] = useState(initialAdvancedOpen)

  return (
    <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
      <div className='flex items-center gap-2 border-b border-border/50 pb-2 text-xs font-semibold text-foreground'>
        <Radio className='size-3.5 text-primary' />
        <span>{t('sections.mqttBroker')}</span>
      </div>

      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        <FormField
          control={control}
          name='broker'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-xs font-medium'>
                {t('fields.mqttBroker')}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('fields.mqttBrokerPlaceholder')}
                  className='font-mono text-xs'
                  autoComplete='off'
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='topic'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-xs font-medium'>
                {t('fields.mqttTopic')}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('fields.mqttTopicPlaceholder')}
                  className='font-mono text-xs'
                  autoComplete='off'
                />
              </FormControl>
              <FormDescription className='text-[11px] text-muted-foreground'>
                {t('fields.mqttTopicHint')}
              </FormDescription>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />
      </div>

      {/* 可选 MQTT 认证 */}
      <div className='grid grid-cols-1 gap-3 pt-1 sm:grid-cols-2'>
        <FormField
          control={control}
          name='mqttUser'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-xs font-medium'>
                {t('fields.mqttUsername')}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('fields.mqttUsernamePlaceholder')}
                  className='font-mono text-xs'
                  autoComplete='off'
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='mqttPass'
          render={({ field }) => (
            <FormItem>
              <FormLabel className='text-xs font-medium'>
                {t('fields.mqttPassword')}
              </FormLabel>
              <FormControl>
                <Input
                  {...field}
                  type='password'
                  placeholder={
                    isEdit && secretSet
                      ? t('fields.secretRetained')
                      : t('fields.mqttPasswordPlaceholder')
                  }
                  className='font-mono text-xs'
                  autoComplete='off'
                />
              </FormControl>
              <FormMessage className='text-xs' />
            </FormItem>
          )}
        />
      </div>

      {/* 可折叠的 MQTT 高级选项 */}
      <Collapsible
        open={advancedOpen}
        onOpenChange={setAdvancedOpen}
        className='space-y-3 rounded-lg border border-border/60 bg-muted/20 p-3 transition-colors'
      >
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
            <Sliders className='size-3.5 text-muted-foreground' />
            <span>{t('advanced.mqttTitle')}</span>
            <MqttAdvancedSummary control={control} />
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
              <span className='sr-only'>{t('actions.toggleAdvancedMqtt')}</span>
            </Button>
          </CollapsibleTrigger>
        </div>

        <CollapsibleContent className='space-y-3 pt-1'>
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
            <FormField
              control={control}
              name='clientId'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.mqttClientId')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      maxLength={23}
                      placeholder={t('fields.mqttClientIdPlaceholder')}
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
              name='qos'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.mqttQoS')}
                  </FormLabel>
                  <Select
                    value={String(field.value)}
                    onValueChange={(val) => field.onChange(Number(val))}
                  >
                    <FormControl>
                      <SelectTrigger className='bg-background font-mono text-xs'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='0'>{t('fields.mqttQoS0')}</SelectItem>
                      <SelectItem value='1'>{t('fields.mqttQoS1')}</SelectItem>
                      <SelectItem value='2'>{t('fields.mqttQoS2')}</SelectItem>
                    </SelectContent>
                  </Select>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />

            <FormField
              control={control}
              name='keepAliveSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.mqttKeepAlive')} ({t('fields.mqttKeepAliveUnit')}
                    )
                  </FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={5}
                      max={300}
                      value={Number.isNaN(field.value) ? '' : field.value}
                      onChange={(e) => {
                        const raw = e.target.value
                        field.onChange(raw === '' ? Number.NaN : Number(raw))
                      }}
                      className='bg-background font-mono text-xs'
                    />
                  </FormControl>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={control}
            name='tlsSkipVerify'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border border-border/60 bg-background/80 p-3'>
                <div className='space-y-0.5'>
                  <FormLabel className='cursor-pointer text-xs font-medium'>
                    {t('fields.tlsSkipVerify')}
                  </FormLabel>
                  <FormDescription className='text-[11px] text-muted-foreground'>
                    {t('fields.tlsSkipVerifyDesc')}
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
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}
