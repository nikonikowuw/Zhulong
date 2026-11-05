import { type UseFormReturn } from 'react-hook-form'
import { RadioTower, Video } from 'lucide-react'
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
import { NumberInput } from '@/components/ui/number-input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { type Gb28181ConfigFormValues } from '../data/config-form-schema'
import { SIP_TRANSPORTS } from '../data/schema'

interface Gb28181ConfigFieldsProps {
  form: UseFormReturn<Gb28181ConfigFormValues>
  /** 未启用时不绑定端口，媒体相关字段整体禁用。 */
  isEnabled: boolean
}

/**
 * 配置表单的字段网格。
 *
 * 将信令参数与媒体参数分栏呈现，建立清晰的视觉动线，
 * 避免扁平堆砌导致的说明文案喧宾夺主。
 */
export function Gb28181ConfigFields({
  form,
  isEnabled,
}: Gb28181ConfigFieldsProps) {
  const { t } = useTranslation('gb28181')

  return (
    <div className='space-y-4'>
      {/* 分组一：SIP 信令与服务身份 */}
      <div className='space-y-4 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
        <div className='flex items-center gap-2 border-b border-border/50 pb-2.5 text-xs font-semibold text-foreground'>
          <RadioTower className='size-4 text-primary' />
          <span>
            {t('groups.signalling', { defaultValue: 'SIP 信令与服务端身份' })}
          </span>
        </div>

        <div className='grid gap-4 md:grid-cols-2'>
          <FormField
            control={form.control}
            name='sipId'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.sipId')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder={t('config.sipIdPlaceholder')}
                    autoComplete='off'
                    spellCheck={false}
                    className='font-mono'
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.sipIdDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='sipRealm'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.sipRealm')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder={t('config.sipRealmPlaceholder')}
                    autoComplete='off'
                    spellCheck={false}
                    className='font-mono'
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.sipRealmDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='sipListen'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.sipListen')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='0.0.0.0:5060'
                    autoComplete='off'
                    spellCheck={false}
                    className='font-mono'
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.sipListenDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='sipTransport'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.sipTransport')}
                </FormLabel>
                <Select value={field.value} onValueChange={field.onChange}>
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {SIP_TRANSPORTS.map((transport) => (
                      <SelectItem key={transport} value={transport}>
                        {t(`transport.${transport}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.sipTransportDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      </div>

      {/* 分组二：媒体传输与端口池 */}
      <div className='space-y-4 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
        <div className='flex items-center gap-2 border-b border-border/50 pb-2.5 text-xs font-semibold text-foreground'>
          <Video className='size-4 text-primary' />
          <span>{t('groups.media', { defaultValue: '媒体传输与端口池' })}</span>
        </div>

        <div className='grid gap-4 md:grid-cols-2'>
          <FormField
            control={form.control}
            name='mediaBindAddress'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.mediaBindAddress')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder='0.0.0.0'
                    autoComplete='off'
                    spellCheck={false}
                    className='font-mono'
                    disabled={!isEnabled}
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.mediaBindAddressDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='mediaAdvertisedIp'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.mediaAdvertisedIp')}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    placeholder={t('config.mediaAdvertisedIpPlaceholder')}
                    autoComplete='off'
                    spellCheck={false}
                    className='font-mono'
                    disabled={!isEnabled}
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.mediaAdvertisedIpDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='rtpPortMin'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.rtpPortMin')}
                </FormLabel>
                <FormControl>
                  <NumberInput
                    value={field.value}
                    integer
                    min={1}
                    max={65535}
                    onValueCommit={field.onChange}
                    disabled={!isEnabled}
                    className='h-9 w-full px-3 text-start font-mono text-sm'
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.rtpPortMinDesc', {
                    defaultValue: 'RTP/RTCP 媒体流接收起始端口',
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='rtpPortMax'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='text-xs font-medium'>
                  {t('config.rtpPortMax')}
                </FormLabel>
                <FormControl>
                  <NumberInput
                    value={field.value}
                    integer
                    min={1}
                    max={65535}
                    onValueCommit={field.onChange}
                    disabled={!isEnabled}
                    className='h-9 w-full px-3 text-start font-mono text-sm'
                  />
                </FormControl>
                <FormDescription className='text-xs leading-relaxed'>
                  {t('config.rtpPortMaxDesc', {
                    defaultValue: 'RTP/RTCP 媒体流接收结束端口',
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      </div>
    </div>
  )
}
