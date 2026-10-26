import { type UseFormReturn } from 'react-hook-form'
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
 * 与外壳（启用开关、容量提示、P0 固定项、保存/重置）拆开，外壳负责提交与
 * 重启语义，这里只负责字段与校验文案（规范 §1.2）。
 */
export function Gb28181ConfigFields({
  form,
  isEnabled,
}: Gb28181ConfigFieldsProps) {
  const { t } = useTranslation('gb28181')

  return (
    <div className='grid gap-4 md:grid-cols-2'>
      <FormField
        control={form.control}
        name='sipId'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.sipId')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                placeholder={t('config.sipIdPlaceholder')}
                autoComplete='off'
                spellCheck={false}
              />
            </FormControl>
            <FormDescription>{t('config.sipIdDesc')}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='sipRealm'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.sipRealm')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                placeholder={t('config.sipRealmPlaceholder')}
                autoComplete='off'
                spellCheck={false}
              />
            </FormControl>
            <FormDescription>{t('config.sipRealmDesc')}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='sipListen'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.sipListen')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                placeholder='0.0.0.0:5060'
                autoComplete='off'
                spellCheck={false}
                className='font-mono'
              />
            </FormControl>
            <FormDescription>{t('config.sipListenDesc')}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='sipTransport'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.sipTransport')}</FormLabel>
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
            <FormDescription>{t('config.sipTransportDesc')}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='mediaBindAddress'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.mediaBindAddress')}</FormLabel>
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
            <FormDescription>
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
            <FormLabel>{t('config.mediaAdvertisedIp')}</FormLabel>
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
            <FormDescription>
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
            <FormLabel>{t('config.rtpPortMin')}</FormLabel>
            <FormControl>
              <NumberInput
                value={field.value}
                integer
                min={1}
                max={65535}
                onValueCommit={field.onChange}
                disabled={!isEnabled}
                className='h-9 w-full px-3 text-start text-sm'
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='rtpPortMax'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('config.rtpPortMax')}</FormLabel>
            <FormControl>
              <NumberInput
                value={field.value}
                integer
                min={1}
                max={65535}
                onValueCommit={field.onChange}
                disabled={!isEnabled}
                className='h-9 w-full px-3 text-start text-sm'
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  )
}
