import { useWatch, type Control, type UseFormSetValue } from 'react-hook-form'
import { Globe, Lock, Radio } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import type { AlertDestinationFormValues } from '../../data/schema'
import type { ChannelType } from '../../types'

interface ChannelTypeCardsProps {
  control: Control<AlertDestinationFormValues>
  setValue: UseFormSetValue<AlertDestinationFormValues>
  isEdit: boolean
  disabled?: boolean
  onResetTestResult?: () => void
}

export function ChannelTypeCards({
  control,
  setValue,
  isEdit,
  disabled = false,
  onResetTestResult,
}: ChannelTypeCardsProps) {
  const { t } = useTranslation('alertPush')
  const channelLocked = isEdit || disabled

  const channelType = useWatch({
    control,
    name: 'channelType',
  })

  const imageMode = useWatch({
    control,
    name: 'imageMode',
  })

  const handleProtocolChange = (nextChannel: ChannelType) => {
    if (channelLocked) return
    setValue('channelType', nextChannel)
    if (nextChannel === 'webhook' && imageMode === 'base64') {
      setValue('imageMode', 'multipart')
    } else if (nextChannel === 'mqtt' && imageMode === 'multipart') {
      setValue('imageMode', 'base64')
    }
    onResetTestResult?.()
  }

  return (
    <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
      {/* 通道名称 */}
      <FormField
        control={control}
        name='name'
        render={({ field }) => (
          <FormItem>
            <FormLabel className='text-xs font-semibold'>
              {t('fields.name')}
            </FormLabel>
            <FormControl>
              <Input
                {...field}
                placeholder={t('fields.namePlaceholder')}
                autoComplete='off'
                className='h-9 text-xs sm:text-sm'
              />
            </FormControl>
            <FormMessage className='text-xs' />
          </FormItem>
        )}
      />

      {/* 协议类型：双列单选卡片 */}
      <div className='space-y-2 pt-1'>
        <div className='flex items-center justify-between'>
          <Label className='text-xs font-semibold'>
            {t('sections.channelSelect')}
          </Label>
          {isEdit ? (
            <span className='flex items-center gap-1 text-[11px] text-muted-foreground'>
              <Lock className='size-3' />
              {t('dialog.channelLockedNotice')}
            </span>
          ) : null}
        </div>

        <RadioGroup
          value={channelType}
          onValueChange={(val) => handleProtocolChange(val as ChannelType)}
          disabled={channelLocked}
          className='grid grid-cols-1 gap-3 sm:grid-cols-2'
        >
          {/* Webhook Card */}
          <Label
            htmlFor='protocol-webhook'
            className={cn(
              'relative flex cursor-pointer flex-col justify-between rounded-xl border p-3.5 shadow-2xs transition-all',
              channelType === 'webhook'
                ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                : 'border-border/80 bg-card/60 hover:border-border hover:bg-muted/30',
              channelLocked && 'cursor-default opacity-85'
            )}
          >
            <div className='flex items-start justify-between gap-2'>
              <div className='flex size-8 items-center justify-center rounded-lg border border-border/70 bg-background text-foreground shadow-2xs'>
                <Globe className='size-4 text-primary' />
              </div>
              <RadioGroupItem
                value='webhook'
                id='protocol-webhook'
                className='mt-0.5'
                disabled={channelLocked}
              />
            </div>
            <div className='mt-2.5 space-y-1'>
              <span className='text-xs font-semibold text-foreground'>
                {t('channelTypes.webhook')}
              </span>
              <p className='text-[11px] leading-relaxed text-muted-foreground'>
                {t('channelTypes.webhookDesc')}
              </p>
            </div>
          </Label>

          {/* MQTT Card */}
          <Label
            htmlFor='protocol-mqtt'
            className={cn(
              'relative flex cursor-pointer flex-col justify-between rounded-xl border p-3.5 shadow-2xs transition-all',
              channelType === 'mqtt'
                ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                : 'border-border/80 bg-card/60 hover:border-border hover:bg-muted/30',
              channelLocked && 'cursor-default opacity-85'
            )}
          >
            <div className='flex items-start justify-between gap-2'>
              <div className='flex size-8 items-center justify-center rounded-lg border border-border/70 bg-background text-foreground shadow-2xs'>
                <Radio className='size-4 text-primary' />
              </div>
              <RadioGroupItem
                value='mqtt'
                id='protocol-mqtt'
                className='mt-0.5'
                disabled={channelLocked}
              />
            </div>
            <div className='mt-2.5 space-y-1'>
              <span className='text-xs font-semibold text-foreground'>
                {t('channelTypes.mqtt')}
              </span>
              <p className='text-[11px] leading-relaxed text-muted-foreground'>
                {t('channelTypes.mqttDesc')}
              </p>
            </div>
          </Label>
        </RadioGroup>
      </div>
    </div>
  )
}
