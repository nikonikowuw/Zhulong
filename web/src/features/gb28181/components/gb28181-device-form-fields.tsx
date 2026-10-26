import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import {
  Form,
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
import { PasswordInput } from '@/components/password-input'
import {
  createGb28181DeviceFormSchema,
  toGb28181DeviceFormValues,
  toGb28181DevicePayload,
  type Gb28181DeviceFormValues,
} from '../data/device-form-schema'
import {
  DEVICE_EXPIRES_MAX,
  DEVICE_EXPIRES_MIN,
  DEVICE_TRANSPORTS,
  type Gb28181Device,
  type UpsertGb28181DeviceRequest,
} from '../data/schema'
import { describeGb28181Error } from '../utils/error-message'

/** 设备预置表单本体：弹窗 footer 的提交按钮在表单外，靠 `formId` 关联。 */
interface Gb28181DeviceFormFieldsProps {
  device: Gb28181Device | null
  isPending: boolean
  /** 弹窗 footer 的提交按钮引用的表单 id。 */
  formId: string
  onSubmit: (
    deviceId: string,
    payload: UpsertGb28181DeviceRequest
  ) => Promise<void>
  onClose: () => void
}

export function Gb28181DeviceFormFields({
  device,
  isPending,
  formId,
  onSubmit,
  onClose,
}: Gb28181DeviceFormFieldsProps) {
  const { t } = useTranslation('gb28181')
  const isEdit = device !== null
  const schema = useMemo(
    () => createGb28181DeviceFormSchema(t, { isEdit }),
    [t, isEdit]
  )

  const form = useForm<Gb28181DeviceFormValues>({
    resolver: zodResolver(schema),
    defaultValues: toGb28181DeviceFormValues(device),
  })

  const handleSubmit = async (values: Gb28181DeviceFormValues) => {
    if (isPending) return
    const deviceId = values.deviceId.trim()
    try {
      await onSubmit(deviceId, toGb28181DevicePayload(values))
      toast.success(
        isEdit
          ? t('devices.saveSuccess', { deviceId })
          : t('devices.createSuccess', { deviceId })
      )
      onClose()
    } catch (error: unknown) {
      handleFormError(form, error, {
        onUnhandled: () => {
          toast.error(describeGb28181Error(error, t))
        },
      })
    }
  }

  return (
    <Form {...form}>
      <form
        id={formId}
        onSubmit={form.handleSubmit(handleSubmit)}
        className='space-y-4'
      >
        <FormField
          control={form.control}
          name='deviceId'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('devices.form.deviceId')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder='34020000001320000001'
                  autoComplete='off'
                  spellCheck={false}
                  className='font-mono'
                  // 设备编号是主键：设备已注册后再改会变成另一条记录。
                  disabled={isEdit || isPending}
                  readOnly={isEdit}
                />
              </FormControl>
              <FormDescription>
                {t('devices.form.deviceIdDesc')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='name'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('devices.form.name')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('devices.form.namePlaceholder')}
                  autoComplete='off'
                />
              </FormControl>
              <FormDescription>{t('devices.form.nameDesc')}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='password'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('devices.form.password')}</FormLabel>
              <FormControl>
                <PasswordInput
                  {...field}
                  placeholder={
                    isEdit
                      ? t('devices.form.passwordKeepPlaceholder')
                      : t('devices.form.passwordPlaceholder')
                  }
                  autoComplete='new-password'
                  disabled={isPending}
                />
              </FormControl>
              <FormDescription>
                {isEdit
                  ? t('devices.form.passwordKeepHint')
                  : t('devices.form.passwordDesc')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <div className='grid gap-4 sm:grid-cols-2'>
          <FormField
            control={form.control}
            name='transport'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('devices.form.transport')}</FormLabel>
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                  disabled={isPending}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {DEVICE_TRANSPORTS.map((transport) => (
                      <SelectItem key={transport} value={transport}>
                        {t(`transport.${transport}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t('devices.form.transportDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='expiresSeconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('devices.form.expires')}</FormLabel>
                <FormControl>
                  <NumberInput
                    value={field.value}
                    integer
                    min={DEVICE_EXPIRES_MIN}
                    max={DEVICE_EXPIRES_MAX}
                    onValueCommit={field.onChange}
                    disabled={isPending}
                    className='h-9 w-full px-3 text-start text-sm'
                  />
                </FormControl>
                <FormDescription>
                  {t('devices.form.expiresDesc')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      </form>
    </Form>
  )
}
