import { type Control, useWatch } from 'react-hook-form'
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { type CameraFormValues } from '../data/schema'

interface CamerasActionStreamFieldsProps {
  control: Control<CameraFormValues>
}

export function CamerasActionStreamFields({
  control,
}: CamerasActionStreamFieldsProps) {
  const { t } = useTranslation('cameras')
  const hasSub = useWatch({
    control,
    name: 'hasSubStream',
  })

  return (
    <div className='space-y-4'>
      {/* 主流 RTSP URL */}
      <FormField
        control={control}
        name='mainRtspUrl'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('form.mainRtsp')}</FormLabel>
            <FormControl>
              <Input
                placeholder={t('form.mainRtspPlaceholder')}
                className='font-mono'
                autoComplete='off'
                {...field}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />

      {/* 主流传输协议 */}
      <FormField
        control={control}
        name='mainTransport'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('form.mainTransport')}</FormLabel>
            <Select onValueChange={field.onChange} value={field.value}>
              <FormControl>
                <SelectTrigger className='w-full'>
                  <SelectValue />
                </SelectTrigger>
              </FormControl>
              <SelectContent>
                <SelectItem value='tcp'>{t('form.transportTcp')}</SelectItem>
                <SelectItem value='udp'>{t('form.transportUdp')}</SelectItem>
              </SelectContent>
            </Select>
            <FormMessage />
          </FormItem>
        )}
      />

      {/* 子流开关与配置 */}
      <FormField
        control={control}
        name='hasSubStream'
        render={({ field }) => (
          <FormItem className='flex flex-row items-center justify-between rounded-lg border border-border p-3'>
            <div className='space-y-0.5'>
              <FormLabel className='text-sm font-medium'>
                {t('table.columns.subStream')}
              </FormLabel>
              <FormDescription className='text-xs'>
                {t('form.hasSubStream')}
              </FormDescription>
            </div>
            <FormControl>
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            </FormControl>
          </FormItem>
        )}
      />

      {hasSub ? (
        <div className='space-y-4 rounded-lg border border-border/80 bg-muted/30 p-3.5'>
          <FormField
            control={control}
            name='subRtspUrl'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('form.subRtsp')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('form.subRtspPlaceholder')}
                    className='font-mono'
                    autoComplete='off'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={control}
            name='subTransport'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('form.subTransport')}</FormLabel>
                <Select onValueChange={field.onChange} value={field.value}>
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    <SelectItem value='tcp'>
                      {t('form.transportTcp')}
                    </SelectItem>
                    <SelectItem value='udp'>
                      {t('form.transportUdp')}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      ) : null}
    </div>
  )
}
