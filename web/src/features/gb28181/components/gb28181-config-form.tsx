import { useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Info, Lock, RotateCcw, Save } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import {
  createGb28181ConfigFormSchema,
  toGb28181ConfigFormValues,
  toGb28181ConfigPayload,
  type Gb28181ConfigFormValues,
} from '../data/config-form-schema'
import {
  mediaCapacity,
  SUPPORTED_SECURITY_PROFILE,
  type Gb28181Config,
  type Gb28181ConfigResponse,
  type Gb28181RuntimeInfo,
} from '../data/schema'
import { describeGb28181Error } from '../utils/error-message'
import { Gb28181ConfigFields } from './gb28181-config-fields'
import { Gb28181RestartDiff } from './gb28181-restart-diff'
import { Gb28181StatusBadge } from './gb28181-status-badge'

interface Gb28181ConfigFormProps {
  config: Gb28181Config
  runtime: Gb28181RuntimeInfo
  isSaving: boolean
  onSave: (payload: Gb28181Config) => Promise<Gb28181ConfigResponse>
}

/**
 * 目标配置表单。
 *
 * 保存与生效是两件事：这里写的是「待生效配置」，运行中的 SIP listener
 * 仍使用启动时加载的 `runtime.startupConfig`。因此面板同时展示两者差异，
 * 且只有 `restartRequired` 被运行时报告时才宣称需要重启。
 */
export function Gb28181ConfigForm({
  config,
  runtime,
  isSaving,
  onSave,
}: Gb28181ConfigFormProps) {
  const { t } = useTranslation('gb28181')
  const schema = useMemo(() => createGb28181ConfigFormSchema(t), [t])

  const form = useForm<Gb28181ConfigFormValues>({
    resolver: zodResolver(schema),
    defaultValues: toGb28181ConfigFormValues(config),
  })

  const isEnabled = useWatch({ control: form.control, name: 'enabled' })
  const portMin = useWatch({ control: form.control, name: 'rtpPortMin' })
  const portMax = useWatch({ control: form.control, name: 'rtpPortMax' })

  const capacity = mediaCapacity(portMin, portMax)
  const isDirty = form.formState.isDirty

  const onSubmit = async (values: Gb28181ConfigFormValues) => {
    if (isSaving) return
    try {
      const saved = await onSave(toGb28181ConfigPayload(values))
      // 服务端可能补齐字段（例如空通告地址被推导出实际 IPv4），
      // 因此以响应为准重置表单，避免界面停留在过期的输入值。
      form.reset(toGb28181ConfigFormValues(saved.config))
      toast.success(t('config.saved'))
    } catch (error: unknown) {
      handleFormError(form, error, {
        onUnhandled: () => {
          toast.error(describeGb28181Error(error, t))
        },
      })
    }
  }

  return (
    <section className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-0.5'>
          <h3 className='text-sm font-semibold tracking-tight'>
            {t('sections.config')}
          </h3>
          <p className='text-xs text-muted-foreground'>{t('config.desc')}</p>
        </div>
        {isDirty && (
          <Gb28181StatusBadge
            level='warning'
            label={t('config.unsaved')}
            className='h-6 font-medium'
          />
        )}
      </div>

      <Gb28181RestartDiff
        restartRequired={runtime.restartRequired}
        desired={config}
        startup={runtime.startupConfig}
      />

      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
          {/* 总开关：卡片化呈现 */}
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-xl border border-border/80 bg-card/75 p-4 shadow-2xs'>
                <div className='space-y-0.5'>
                  <FormLabel className='text-sm font-semibold text-foreground'>
                    {t('config.enabled')}
                  </FormLabel>
                  <FormDescription className='text-xs text-muted-foreground'>
                    {t('config.enabledDesc')}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    aria-label={t('config.enabled')}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <Gb28181ConfigFields form={form} isEnabled={isEnabled} />

          {/* 端口容量推算提示 */}
          <div
            className={cn(
              'flex items-center gap-2 rounded-xl border border-border/60 bg-muted/30 px-3.5 py-2.5 text-xs text-muted-foreground',
              capacity < 1 && statusLevelClass('destructive')
            )}
          >
            <Info className='size-4 shrink-0 text-primary' />
            <p className='leading-relaxed'>
              {t('config.portSpanHint', { count: capacity })}
            </p>
          </div>

          {/* 协议与安全策略固定项 */}
          <div className='space-y-2.5 rounded-xl border border-border/70 bg-card/50 p-4 shadow-2xs'>
            <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
              <Lock className='size-3.5 text-muted-foreground' />
              <h4>{t('config.fixedTitle')}</h4>
            </div>
            {/* 这两项不可编辑，且保存时由 `toGb28181ConfigPayload` 强制写成 P0 取值，
                因此这里展示的必须就是「保存后会发生什么」，而不是库里可能残留的旧值。 */}
            <dl className='grid grid-cols-1 gap-2 text-xs sm:grid-cols-2'>
              <div className='flex items-center justify-between rounded-lg border border-border/40 bg-muted/20 px-3 py-2'>
                <dt className='text-muted-foreground'>
                  {t('config.securityProfile')}
                </dt>
                <dd className='font-mono font-medium text-foreground'>
                  {SUPPORTED_SECURITY_PROFILE}
                </dd>
              </div>
              <div className='flex items-center justify-between rounded-lg border border-border/40 bg-muted/20 px-3 py-2'>
                <dt className='text-muted-foreground'>
                  {t('config.requireRegisteredDevice')}
                </dt>
                <dd className='font-mono font-medium text-foreground'>
                  {t('value.on')}
                </dd>
              </div>
            </dl>
            <p className='text-xs leading-relaxed text-muted-foreground/80'>
              {t('config.fixedDesc')}
            </p>
          </div>

          <div className='flex justify-end gap-2.5 pt-1'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => form.reset(toGb28181ConfigFormValues(config))}
              disabled={isSaving || !isDirty}
              className='h-8 gap-1.5 text-xs'
            >
              <RotateCcw className='size-3.5' />
              {t('actions.reset')}
            </Button>
            <Button
              type='submit'
              size='sm'
              disabled={isSaving}
              className='h-8 gap-1.5 text-xs shadow-2xs'
            >
              <Save className='size-3.5' />
              {isSaving ? t('actions.saving') : t('actions.save')}
            </Button>
          </div>
        </form>
      </Form>
    </section>
  )
}
