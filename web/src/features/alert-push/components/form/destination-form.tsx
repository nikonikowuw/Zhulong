import { useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, CheckCircle2, Loader2, Play } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'
import { Form } from '@/components/ui/form'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { alertPushApi } from '../../api/alert-push-api'
import {
  createAlertDestinationFormSchema,
  type AlertDestinationFormValues,
} from '../../data/schema'
import type {
  AlertDestination,
  CreateAlertDestinationInput,
  TestDestinationResult,
  UpdateAlertDestinationInput,
} from '../../types'
import { ChannelTypeCards } from './channel-type-cards'
import { DestinationFiltersSection } from './destination-filters-section'
import { HeartbeatCapabilitySection } from './heartbeat-capability-section'
import { MqttConfigSection } from './mqtt-config-section'
import { WebhookConfigSection } from './webhook-config-section'

interface DestinationFormProps {
  destination?: AlertDestination | null
  onCancel: () => void
  onSuccess: () => void
}

export function DestinationForm({
  destination,
  onCancel,
  onSuccess,
}: DestinationFormProps) {
  const { t } = useTranslation('alertPush')
  const isEdit = Boolean(destination)

  // Top level errors
  const [conflictError, setConflictError] = useState<string | null>(null)
  const [unhandledError, setUnhandledError] = useState<string | null>(null)

  // Connection testing state
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<TestDestinationResult | null>(
    null
  )

  // 初始折叠展开判定：若编辑已有配置且存在自定义标头/过滤器，自动展开
  const hasCustomHeaders = Boolean(
    destination?.headerNames && destination.headerNames.length > 0
  )
  const hasCustomFilters = Boolean(
    (destination?.filter?.eventTypes &&
      destination.filter.eventTypes.length > 0) ||
    (destination?.filter?.algorithmIds &&
      destination.filter.algorithmIds.length > 0) ||
    (destination?.filter?.cameraIds && destination.filter.cameraIds.length > 0)
  )

  const schema = useMemo(
    () =>
      createAlertDestinationFormSchema(
        t,
        isEdit,
        Boolean(destination?.secretSet)
      ),
    [t, isEdit, destination?.secretSet]
  )

  const defaultValues: AlertDestinationFormValues = useMemo(() => {
    const isWebhook = destination?.channelType === 'webhook'
    const isMqtt = destination?.channelType === 'mqtt'

    return {
      name: destination?.name || '',
      channelType: destination?.channelType || 'webhook',
      eventsEnabled: destination ? destination.eventsEnabled : true,
      heartbeatEnabled: destination ? destination.heartbeatEnabled : false,
      imageMode:
        destination?.imageMode ||
        (destination?.channelType === 'mqtt' ? 'base64' : 'multipart'),
      heartbeatIntervalSeconds:
        destination?.config.heartbeatIntervalSeconds || 60,
      // Webhook
      url: (isWebhook ? destination.config.url : '') || '',
      heartbeatUrl: (isWebhook ? destination.config.heartbeatUrl : '') || '',
      authType: (isWebhook ? destination.config.authType : 'none') || 'none',
      timeoutSeconds: (isWebhook ? destination.config.timeoutSeconds : 5) || 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: (destination?.headerNames || []).map((k) => ({
        key: k,
        value: '',
      })),
      // MQTT
      broker:
        (isMqtt ? destination.config.broker : '') || 'tcp://10.0.0.1:1883',
      topic: (isMqtt ? destination.config.topic : '') || 'zhulong/alarms',
      heartbeatTopic: (isMqtt ? destination.config.heartbeatTopic : '') || '',
      lwtEnabled: isMqtt ? (destination.config.lwtEnabled ?? true) : true,
      clientId: (isMqtt ? destination.config.clientId : '') || '',
      qos: (isMqtt ? destination.config.qos : 0) ?? 0,
      keepAliveSeconds:
        (isMqtt ? destination.config.keepAliveSeconds : 30) || 30,
      mqttUser: (isMqtt ? destination.config.username : '') || '',
      mqttPass: '',
      tlsSkipVerify:
        (isMqtt ? destination.config.tlsSkipVerify : false) || false,
      // Filter
      eventTypes: destination?.filter?.eventTypes || [],
      algorithmIds: destination?.filter?.algorithmIds || [],
      cameraIds: destination?.filter?.cameraIds || [],
    }
  }, [destination])

  const form = useForm<AlertDestinationFormValues>({
    resolver: zodResolver(schema),
    defaultValues,
  })

  const channelType = useWatch({
    control: form.control,
    name: 'channelType',
  })

  const buildProtocolConfig = (
    values: AlertDestinationFormValues,
    includeEmptyOverrideForClear: boolean
  ): Record<string, unknown> => {
    if (values.channelType === 'webhook') {
      const hbUrl = values.heartbeatUrl.trim()
      return {
        url: values.url.trim(),
        heartbeatUrl: includeEmptyOverrideForClear ? hbUrl : hbUrl || undefined,
        authType: values.authType,
        timeoutSeconds: values.timeoutSeconds,
        heartbeatIntervalSeconds: values.heartbeatIntervalSeconds,
      }
    }

    const hbTopic = values.heartbeatTopic.trim()
    return {
      broker: values.broker.trim(),
      clientId: values.clientId.trim() || undefined,
      username: values.mqttUser.trim() || undefined,
      topic: values.topic.trim(),
      heartbeatTopic: includeEmptyOverrideForClear
        ? hbTopic
        : hbTopic || undefined,
      lwtEnabled: values.lwtEnabled,
      qos: values.qos,
      keepAliveSeconds: values.keepAliveSeconds,
      tlsSkipVerify: values.tlsSkipVerify,
      heartbeatIntervalSeconds: values.heartbeatIntervalSeconds,
    }
  }

  // 连通性测试
  const handleRunTest = async () => {
    const values = form.getValues()
    const hdrsObj: Record<string, string> = {}
    for (const h of values.headers || []) {
      if (h.key.trim() && h.value) {
        hdrsObj[h.key.trim()] = h.value
      }
    }

    setTesting(true)
    setTestResult(null)
    setConflictError(null)
    setUnhandledError(null)

    try {
      const res = await alertPushApi.test({
        id: destination?.id,
        channelType: values.channelType,
        eventsEnabled: values.eventsEnabled,
        heartbeatEnabled: values.heartbeatEnabled,
        imageMode: values.imageMode,
        config: buildProtocolConfig(values, isEdit),
        secret:
          values.channelType === 'webhook'
            ? {
                token: values.token ? values.token : undefined,
                username: values.basicUser ? values.basicUser : undefined,
                password: values.basicPass ? values.basicPass : undefined,
                headers: Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
              }
            : {
                password: values.mqttPass ? values.mqttPass : undefined,
              },
      })
      setTestResult(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setTestResult({ ok: false, latencyMs: 0, reason: msg })
    } finally {
      setTesting(false)
    }
  }

  // 提交保存
  const onSubmit = async (values: AlertDestinationFormValues) => {
    setConflictError(null)
    setUnhandledError(null)

    const hdrsObj: Record<string, string> = {}
    for (const h of values.headers || []) {
      if (h.key.trim() && h.value) {
        hdrsObj[h.key.trim()] = h.value
      }
    }

    try {
      if (isEdit && destination) {
        const updatePayload: UpdateAlertDestinationInput = {
          name: values.name.trim(),
          eventsEnabled: values.eventsEnabled,
          heartbeatEnabled: values.heartbeatEnabled,
          imageMode: values.imageMode,
          filter: {
            eventTypes: values.eventTypes,
            algorithmIds: values.algorithmIds,
            cameraIds: values.cameraIds,
          },
          config: buildProtocolConfig(values, true),
          secret:
            values.channelType === 'webhook'
              ? {
                  token: values.token ? values.token : undefined,
                  username: values.basicUser ? values.basicUser : undefined,
                  password: values.basicPass ? values.basicPass : undefined,
                  headers:
                    Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
                }
              : {
                  password: values.mqttPass ? values.mqttPass : undefined,
                },
        }
        await alertPushApi.update(destination.id, updatePayload)
        toast.success(t('testSuccess', { latency: 0 }))
      } else {
        const createPayload: CreateAlertDestinationInput = {
          name: values.name.trim(),
          channelType: values.channelType,
          eventsEnabled: values.eventsEnabled,
          heartbeatEnabled: values.heartbeatEnabled,
          imageMode: values.imageMode,
          filter: {
            eventTypes: values.eventTypes,
            algorithmIds: values.algorithmIds,
            cameraIds: values.cameraIds,
          },
          config: buildProtocolConfig(values, false),
          secret:
            values.channelType === 'webhook'
              ? {
                  token: values.token ? values.token : undefined,
                  username: values.basicUser ? values.basicUser : undefined,
                  password: values.basicPass ? values.basicPass : undefined,
                  headers:
                    Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
                }
              : {
                  password: values.mqttPass ? values.mqttPass : undefined,
                },
        }
        await alertPushApi.create(createPayload)
        toast.success(t('dialog.createTitle'))
      }
      onSuccess()
    } catch (err: unknown) {
      handleFormError(form, err, {
        fieldMap: {
          'config.url': 'url',
          'config.heartbeatUrl': 'heartbeatUrl',
          'config.broker': 'broker',
          'config.topic': 'topic',
          'config.heartbeatTopic': 'heartbeatTopic',
          'config.clientId': 'clientId',
        },
        onConflict: (_err, message) => {
          setConflictError(message || '目标配置发生冲突，请核对后重试')
        },
        onUnhandled: (message) => {
          setUnhandledError(message)
        },
      })
    }
  }

  const isSubmitting = form.formState.isSubmitting

  return (
    <Form {...form}>
      <form
        id='alert-destination-form'
        onSubmit={form.handleSubmit(onSubmit)}
        className='space-y-4'
      >
        {/* 全局冲突或未捕获错误横幅 */}
        {conflictError ? (
          <div className='flex items-start gap-2.5 rounded-xl border border-amber-500/30 bg-amber-500/10 p-3 text-xs text-amber-600 dark:text-amber-400'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{conflictError}</div>
          </div>
        ) : null}

        {unhandledError ? (
          <div className='flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{unhandledError}</div>
          </div>
        ) : null}

        {/* 分区 1：基础连接与凭据 */}
        <ChannelTypeCards
          control={form.control}
          setValue={form.setValue}
          isEdit={isEdit}
          onResetTestResult={() => setTestResult(null)}
        />

        {channelType === 'webhook' ? (
          <WebhookConfigSection
            control={form.control}
            isEdit={isEdit}
            secretSet={destination?.secretSet}
            initialAdvancedOpen={hasCustomHeaders}
          />
        ) : (
          <MqttConfigSection
            control={form.control}
            isEdit={isEdit}
            secretSet={destination?.secretSet}
            initialAdvancedOpen={hasCustomHeaders}
          />
        )}

        {/* 分区 2：感知事件推送能力（开关 + 图片交付 + 过滤器） */}
        <DestinationFiltersSection
          control={form.control}
          setValue={form.setValue}
          initialFiltersOpen={hasCustomFilters}
        />

        {/* 分区 3：设备存活上报能力（开关 + 上报间隔 + 分流路由/LWT） */}
        <HeartbeatCapabilitySection control={form.control} />

        {/* 底部 Footer */}
        <DialogFooter className='gap-y-3 border-t border-border/60 pt-3 sm:items-center sm:justify-between'>
          {/* 左侧探测工具与状态指示 */}
          <div className='flex flex-wrap items-center gap-2'>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={testing || isSubmitting}
                  onClick={handleRunTest}
                  className='h-8 gap-1.5 text-xs shadow-2xs'
                >
                  {testing ? (
                    <Loader2 className='size-3.5 animate-spin' />
                  ) : (
                    <Play className='size-3.5' />
                  )}
                  <span>
                    {testing
                      ? t('actions.testing')
                      : t('actions.testConnection')}
                  </span>
                </Button>
              </TooltipTrigger>
              <TooltipContent className='text-xs'>
                {t('dialog.testTooltip')}
              </TooltipContent>
            </Tooltip>

            {/* 连通性测试微药丸徽标 */}
            {testResult ? (
              <Badge
                variant='outline'
                className={cn(
                  'h-7 gap-1.5 text-xs font-normal shadow-2xs',
                  statusLevelClass(testResult.ok ? 'active' : 'destructive')
                )}
              >
                {testResult.ok ? (
                  <CheckCircle2 className='size-3.5 shrink-0' />
                ) : (
                  <span
                    className={cn(
                      'size-1.5 rounded-full',
                      statusDotClass('destructive')
                    )}
                  />
                )}
                <span>
                  {testResult.ok
                    ? t('testSuccess', { latency: testResult.latencyMs })
                    : t('testFailed', {
                        reason: testResult.reason
                          ? t(`failureReasons.${testResult.reason}`, {
                              defaultValue: testResult.reason,
                            })
                          : '',
                      })}
                </span>
              </Badge>
            ) : null}
          </div>

          {/* 右侧动作按钮 */}
          <div className='flex items-center justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={onCancel}
              disabled={isSubmitting}
              className='h-8 text-xs'
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type='submit'
              size='sm'
              disabled={isSubmitting}
              className='h-8 min-w-20 gap-1.5 text-xs shadow-2xs'
            >
              {isSubmitting ? (
                <Loader2 className='size-3.5 animate-spin' />
              ) : null}
              <span>
                {isSubmitting ? t('actions.saving') : t('actions.save')}
              </span>
            </Button>
          </div>
        </DialogFooter>
      </form>
    </Form>
  )
}
