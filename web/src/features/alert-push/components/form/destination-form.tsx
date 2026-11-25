import { useMemo, useRef, useState } from 'react'
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
import { Form } from '@/components/ui/form'
import { SheetFooter } from '@/components/ui/sheet'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { alertPushApi } from '../../api/alert-push-api'
import {
  areHeadersUntouched,
  createAlertDestinationFormSchema,
  INLINE_MAX_KB_DEFAULT,
  type AlertDestinationFormValues,
  type HeaderEntry,
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

function buildWebhookHeadersSecret(
  headers: HeaderEntry[],
  isEdit: boolean,
  originalHeaderNames: string[]
): Record<string, string> | undefined {
  if (isEdit && areHeadersUntouched(headers, originalHeaderNames)) {
    return undefined
  }

  const hdrsObj: Record<string, string> = {}
  for (const h of headers) {
    const key = h.key.trim()
    if (key && h.value) {
      hdrsObj[key] = h.value
    }
  }

  if (Object.keys(hdrsObj).length > 0) {
    return hdrsObj
  }

  // 编辑态下主动清空全部已有标头时，必须显式传空对象 {} 覆盖服务端 map
  if (isEdit && originalHeaderNames.length > 0) {
    return {}
  }

  return undefined
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
  const testSeqRef = useRef(0)

  const originalAuthType =
    destination?.channelType === 'webhook'
      ? destination.config.authType
      : undefined
  const originalHeaderNames = useMemo(
    () => destination?.headerNames ?? [],
    [destination?.headerNames]
  )

  // 初始折叠展开判定：编辑态下若存在对应协议的自定义高级项或过滤器，自动展开
  const hasCustomWebhookAdvanced = Boolean(
    originalHeaderNames.length > 0 ||
    (destination?.channelType === 'webhook' &&
      ((destination.config.timeoutSeconds ?? 5) !== 5 ||
        Boolean(destination.config.tlsSkipVerify)))
  )
  const hasCustomMqttAdvanced = Boolean(
    destination?.channelType === 'mqtt' &&
    ((destination.config.clientId &&
      destination.config.clientId !== `z${destination.id.slice(4)}`) ||
      (destination.config.qos ?? 0) !== 0 ||
      (destination.config.keepAliveSeconds ?? 30) !== 30 ||
      Boolean(destination.config.tlsSkipVerify))
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
        Boolean(destination?.secretSet),
        originalAuthType,
        originalHeaderNames
      ),
    [t, isEdit, destination?.secretSet, originalAuthType, originalHeaderNames]
  )

  const resolver = useMemo(() => zodResolver(schema), [schema])

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
      headers: originalHeaderNames.map((k) => ({
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
      inlineMaxKb:
        (isMqtt ? destination.config.inlineMaxKb : INLINE_MAX_KB_DEFAULT) ||
        INLINE_MAX_KB_DEFAULT,
      mqttUser: (isMqtt ? destination.config.username : '') || '',
      mqttPass: '',
      tlsSkipVerify: Boolean(
        isWebhook
          ? destination.config.tlsSkipVerify
          : isMqtt
            ? destination.config.tlsSkipVerify
            : false
      ),
      // Filter
      eventTypes: destination?.filter?.eventTypes || [],
      algorithmIds: destination?.filter?.algorithmIds || [],
      cameraIds: destination?.filter?.cameraIds || [],
    }
  }, [destination, originalHeaderNames])

  const form = useForm<AlertDestinationFormValues>({
    resolver,
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
        tlsSkipVerify: values.tlsSkipVerify,
        heartbeatIntervalSeconds: values.heartbeatIntervalSeconds,
      }
    }

    const hbTopic = values.heartbeatTopic.trim()
    const trimmedClientId = values.clientId.trim()
    const trimmedMqttUser = values.mqttUser.trim()
    return {
      broker: values.broker.trim(),
      clientId: includeEmptyOverrideForClear
        ? trimmedClientId
        : trimmedClientId || undefined,
      username: includeEmptyOverrideForClear
        ? trimmedMqttUser
        : trimmedMqttUser || undefined,
      topic: values.topic.trim(),
      heartbeatTopic: includeEmptyOverrideForClear
        ? hbTopic
        : hbTopic || undefined,
      lwtEnabled: values.lwtEnabled,
      qos: values.qos,
      keepAliveSeconds: values.keepAliveSeconds,
      tlsSkipVerify: values.tlsSkipVerify,
      // 仅 base64 模式提交；其它模式不改动已保存的预算
      inlineMaxKb:
        values.imageMode === 'base64' ? values.inlineMaxKb : undefined,
      heartbeatIntervalSeconds: values.heartbeatIntervalSeconds,
    }
  }

  // 连通性测试
  const handleRunTest = async () => {
    const seq = ++testSeqRef.current
    const values = form.getValues()
    const headersPayload = buildWebhookHeadersSecret(
      values.headers || [],
      isEdit,
      originalHeaderNames
    )

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
                headers: headersPayload,
              }
            : {
                password: values.mqttPass ? values.mqttPass : undefined,
              },
      })
      if (seq === testSeqRef.current) {
        setTestResult(res)
      }
    } catch (err: unknown) {
      if (seq === testSeqRef.current) {
        const msg = err instanceof Error ? err.message : String(err)
        setTestResult({ ok: false, latencyMs: 0, reason: msg })
      }
    } finally {
      if (seq === testSeqRef.current) {
        setTesting(false)
      }
    }
  }

  // 提交保存
  const onSubmit = async (values: AlertDestinationFormValues) => {
    setConflictError(null)
    setUnhandledError(null)

    const headersPayload = buildWebhookHeadersSecret(
      values.headers || [],
      isEdit,
      originalHeaderNames
    )

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
                  headers: headersPayload,
                }
              : {
                  password: values.mqttPass ? values.mqttPass : undefined,
                },
        }
        await alertPushApi.update(destination.id, updatePayload)
        toast.success(t('toast.updated'))
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
                  headers: headersPayload,
                }
              : {
                  password: values.mqttPass ? values.mqttPass : undefined,
                },
        }
        await alertPushApi.create(createPayload)
        toast.success(t('toast.created'))
      }
      onSuccess()
    } catch (err: unknown) {
      const result = handleFormError(form, err, {
        fieldMap: {
          'config.url': 'url',
          'config.heartbeatUrl': 'heartbeatUrl',
          'config.broker': 'broker',
          'config.topic': 'topic',
          'config.heartbeatTopic': 'heartbeatTopic',
          'config.clientId': 'clientId',
        },
        onConflict: (_err, message) => {
          setConflictError(message || t('validation.conflictFallback'))
        },
        onUnhandled: (message) => {
          setUnhandledError(message)
        },
      })
      if (!result.isAxiosError) {
        setUnhandledError(result.serverMessage)
      }
    }
  }

  const isSubmitting = form.formState.isSubmitting

  return (
    <Form {...form}>
      <form
        id='alert-destination-form'
        onSubmit={form.handleSubmit(onSubmit)}
        className='flex min-h-0 flex-1 flex-col'
      >
        {/* 抽屉滚动区：横幅 + 全部分区，Footer 固定在抽屉底部 */}
        <div
          data-testid='alert-destination-scroll'
          className='min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4'
        >
          {/* 全局冲突或未捕获错误横幅 */}
          {conflictError ? (
            <div
              className={cn(
                'flex items-start gap-2.5 rounded-xl border p-3 text-xs',
                statusLevelClass('warning')
              )}
            >
              <AlertCircle className='mt-0.5 size-4 shrink-0' />
              <div className='flex-1 leading-relaxed'>{conflictError}</div>
            </div>
          ) : null}

          {unhandledError ? (
            <div
              className={cn(
                'flex items-start gap-2.5 rounded-xl border p-3 text-xs',
                statusLevelClass('destructive')
              )}
            >
              <AlertCircle className='mt-0.5 size-4 shrink-0' />
              <div className='flex-1 leading-relaxed'>{unhandledError}</div>
            </div>
          ) : null}

          {/* 分区 1：基础连接与凭据 */}
          <ChannelTypeCards
            control={form.control}
            setValue={form.setValue}
            isEdit={isEdit}
            disabled={testing || isSubmitting}
            onResetTestResult={() => {
              testSeqRef.current += 1
              setTestResult(null)
            }}
          />

          {channelType === 'webhook' ? (
            <WebhookConfigSection
              control={form.control}
              isEdit={isEdit}
              secretSet={destination?.secretSet}
              originalHeaderNames={originalHeaderNames}
              initialAdvancedOpen={hasCustomWebhookAdvanced}
            />
          ) : (
            <MqttConfigSection
              control={form.control}
              isEdit={isEdit}
              secretSet={destination?.secretSet}
              initialAdvancedOpen={hasCustomMqttAdvanced}
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
        </div>

        {/* 底部 Footer */}
        <SheetFooter className='mt-0 shrink-0 gap-y-3 border-t border-border/60 bg-muted/20 px-6 py-3.5 sm:flex-row sm:items-center sm:justify-between'>
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
        </SheetFooter>
      </form>
    </Form>
  )
}
