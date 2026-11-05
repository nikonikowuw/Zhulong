import { useEffect, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  AlertCircle,
  Car,
  Check,
  CheckCircle2,
  ChevronDown,
  Filter,
  Globe,
  Layers,
  Loader2,
  Lock,
  Play,
  Plus,
  Radio,
  Search,
  ShieldAlert,
  Sliders,
  Trash2,
  UserCheck,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { algorithmsApi } from '@/features/algorithms/api/algorithms-api'
import { camerasApi } from '@/features/cameras/api/cameras-api'
import { alertPushApi } from '../api/alert-push-api'
import {
  createAlertDestinationFormSchema,
  RESERVED_HEADERS,
  type AlertDestinationFormValues,
} from '../data/schema'
import type {
  AlertDestination,
  AuthType,
  ChannelType,
  CreateAlertDestinationInput,
  ImageMode,
  TestDestinationResult,
  UpdateAlertDestinationInput,
} from '../types'

const IMAGE_MODE_DESC_KEYS: Record<string, string> = {
  url: 'imageModes.urlDesc',
  multipart: 'imageModes.multipartDesc',
  base64: 'imageModes.base64Desc',
}

interface AlertDestinationDialogProps {
  open: boolean
  destination?: AlertDestination | null
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function AlertDestinationDialog({
  open,
  destination,
  onOpenChange,
  onSuccess,
}: AlertDestinationDialogProps) {
  const { t } = useTranslation('alertPush')
  const isEdit = Boolean(destination)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader className='text-start'>
          <div className='flex items-center gap-3'>
            <div className='flex size-10 items-center justify-center rounded-xl border border-border/80 bg-muted/50 text-primary shadow-2xs'>
              {destination?.channelType === 'mqtt' ? (
                <Radio className='size-5' />
              ) : (
                <Globe className='size-5' />
              )}
            </div>
            <div className='space-y-0.5'>
              <DialogTitle className='text-base font-semibold tracking-tight'>
                {isEdit ? t('dialog.editTitle') : t('dialog.createTitle')}
              </DialogTitle>
              <DialogDescription className='text-xs text-muted-foreground'>
                {isEdit ? t('dialog.editDesc') : t('dialog.createDesc')}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        {open && (
          <DestinationForm
            destination={destination}
            onCancel={() => onOpenChange(false)}
            onSuccess={() => {
              onSuccess()
              onOpenChange(false)
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

interface DestinationFormProps {
  destination?: AlertDestination | null
  onCancel: () => void
  onSuccess: () => void
}

function DestinationForm({
  destination,
  onCancel,
  onSuccess,
}: DestinationFormProps) {
  const { t } = useTranslation('alertPush')
  const isEdit = Boolean(destination)

  // Top level errors
  const [conflictError, setConflictError] = useState<string | null>(null)
  const [unhandledError, setUnhandledError] = useState<string | null>(null)

  // Advanced collapsibles:
  // 默认折叠，保持核心主流程精简一目了然；若编辑已有配置且存在自定义标头/过滤器，自动展开
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

  const [advancedOpen, setAdvancedOpen] = useState(hasCustomHeaders)
  const [filtersOpen, setFiltersOpen] = useState(hasCustomFilters)

  // Candidate data for filters
  const [availableAlgorithms, setAvailableAlgorithms] = useState<
    Array<{ id: string; name: string }>
  >([])
  const [availableCameras, setAvailableCameras] = useState<
    Array<{ id: string; name: string }>
  >([])
  const [algoSearch, setAlgoSearch] = useState('')
  const [camSearch, setCamSearch] = useState('')

  // Connection testing state
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<TestDestinationResult | null>(
    null
  )

  useEffect(() => {
    void algorithmsApi
      .getAlgorithms()
      .then((algos) => {
        setAvailableAlgorithms(algos.map((a) => ({ id: a.id, name: a.name })))
      })
      .catch(() => {})

    void camerasApi
      .getCameras()
      .then((cams) => {
        setAvailableCameras(cams.items.map((c) => ({ id: c.id, name: c.name })))
      })
      .catch(() => {})
  }, [])

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
    return {
      name: destination?.name || '',
      channelType: destination?.channelType || 'webhook',
      imageMode:
        destination?.imageMode ||
        (destination?.channelType === 'mqtt' ? 'base64' : 'multipart'),
      // Webhook
      url: destination?.config?.url || '',
      authType: destination?.config?.authType || 'none',
      timeoutSeconds: destination?.config?.timeoutSeconds || 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: (destination?.headerNames || []).map((k) => ({
        key: k,
        value: '',
      })),
      // MQTT
      broker: destination?.config?.broker || 'tcp://10.0.0.1:1883',
      topic: destination?.config?.topic || 'zhulong/alarms',
      clientId: destination?.config?.clientId || '',
      qos: destination?.config?.qos ?? 0,
      keepAliveSeconds: destination?.config?.keepAliveSeconds || 30,
      mqttUser: destination?.config?.username || '',
      mqttPass: '',
      tlsSkipVerify: destination?.config?.tlsSkipVerify || false,
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

  const imageMode = useWatch({
    control: form.control,
    name: 'imageMode',
  })

  const authType = useWatch({
    control: form.control,
    name: 'authType',
  })

  const timeoutSeconds = useWatch({
    control: form.control,
    name: 'timeoutSeconds',
  })

  const headers = useWatch({
    control: form.control,
    name: 'headers',
  })

  const qos = useWatch({
    control: form.control,
    name: 'qos',
  })

  const keepAliveSeconds = useWatch({
    control: form.control,
    name: 'keepAliveSeconds',
  })

  const clientId = useWatch({
    control: form.control,
    name: 'clientId',
  })

  const selectedEventTypes = useWatch({
    control: form.control,
    name: 'eventTypes',
  })

  const selectedAlgoIds = useWatch({
    control: form.control,
    name: 'algorithmIds',
  })

  const selectedCamIds = useWatch({
    control: form.control,
    name: 'cameraIds',
  })

  // 保证协议切换时，兼容的图片模式同步校准
  const handleProtocolChange = (nextChannel: ChannelType) => {
    if (isEdit) return
    form.setValue('channelType', nextChannel)
    if (nextChannel === 'webhook' && imageMode === 'base64') {
      form.setValue('imageMode', 'multipart')
    } else if (nextChannel === 'mqtt' && imageMode === 'multipart') {
      form.setValue('imageMode', 'base64')
    }
    setTestResult(null)
  }

  // Header 数组操作
  const handleAddHeader = () => {
    const cur = form.getValues('headers') || []
    form.setValue('headers', [...cur, { key: '', value: '' }])
  }

  const handleRemoveHeader = (index: number) => {
    const cur = form.getValues('headers') || []
    form.setValue(
      'headers',
      cur.filter((_, i) => i !== index)
    )
  }

  const handleHeaderChange = (
    index: number,
    field: 'key' | 'value',
    val: string
  ) => {
    const cur = [...(form.getValues('headers') || [])]
    if (cur[index]) {
      cur[index] = { ...cur[index], [field]: val }
      form.setValue('headers', cur)
    }
  }

  // 过滤事件切换
  const toggleEventType = (et: string) => {
    const cur = form.getValues('eventTypes') || []
    const next = cur.includes(et) ? cur.filter((x) => x !== et) : [...cur, et]
    form.setValue('eventTypes', next)
  }

  // 算法选择
  const toggleAlgoSelection = (id: string) => {
    const cur = form.getValues('algorithmIds') || []
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]
    form.setValue('algorithmIds', next)
  }

  const handleSelectAllAlgos = () => {
    form.setValue(
      'algorithmIds',
      availableAlgorithms.map((a) => a.id)
    )
  }

  const handleClearAllAlgos = () => {
    form.setValue('algorithmIds', [])
  }

  // 摄像机选择
  const toggleCamSelection = (id: string) => {
    const cur = form.getValues('cameraIds') || []
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]
    form.setValue('cameraIds', next)
  }

  const handleSelectAllCams = () => {
    form.setValue(
      'cameraIds',
      availableCameras.map((c) => c.id)
    )
  }

  const handleClearAllCams = () => {
    form.setValue('cameraIds', [])
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
        imageMode: values.imageMode,
        config:
          values.channelType === 'webhook'
            ? {
                url: values.url.trim(),
                authType: values.authType,
                timeoutSeconds: values.timeoutSeconds,
              }
            : {
                broker: values.broker.trim(),
                clientId: values.clientId.trim() || undefined,
                username: values.mqttUser.trim() || undefined,
                topic: values.topic.trim(),
                qos: values.qos,
                keepAliveSeconds: values.keepAliveSeconds,
                tlsSkipVerify: values.tlsSkipVerify,
              },
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
          imageMode: values.imageMode,
          filter: {
            eventTypes: values.eventTypes,
            algorithmIds: values.algorithmIds,
            cameraIds: values.cameraIds,
          },
          config:
            values.channelType === 'webhook'
              ? {
                  url: values.url.trim(),
                  authType: values.authType,
                  timeoutSeconds: values.timeoutSeconds,
                }
              : {
                  broker: values.broker.trim(),
                  clientId: values.clientId.trim() || undefined,
                  username: values.mqttUser.trim() || undefined,
                  topic: values.topic.trim(),
                  qos: values.qos,
                  keepAliveSeconds: values.keepAliveSeconds,
                  tlsSkipVerify: values.tlsSkipVerify,
                },
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
          imageMode: values.imageMode,
          filter: {
            eventTypes: values.eventTypes,
            algorithmIds: values.algorithmIds,
            cameraIds: values.cameraIds,
          },
          config:
            values.channelType === 'webhook'
              ? {
                  url: values.url.trim(),
                  authType: values.authType,
                  timeoutSeconds: values.timeoutSeconds,
                }
              : {
                  broker: values.broker.trim(),
                  clientId: values.clientId.trim() || undefined,
                  username: values.mqttUser.trim() || undefined,
                  topic: values.topic.trim(),
                  qos: values.qos,
                  keepAliveSeconds: values.keepAliveSeconds,
                  tlsSkipVerify: values.tlsSkipVerify,
                },
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
          'config.broker': 'broker',
          'config.topic': 'topic',
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

  // 搜索过滤后的算法和摄像机列表
  const filteredAlgos = useMemo(() => {
    if (!algoSearch.trim()) return availableAlgorithms
    const q = algoSearch.toLowerCase()
    return availableAlgorithms.filter((a) => a.name.toLowerCase().includes(q))
  }, [availableAlgorithms, algoSearch])

  const filteredCams = useMemo(() => {
    if (!camSearch.trim()) return availableCameras
    const q = camSearch.toLowerCase()
    return availableCameras.filter((c) => c.name.toLowerCase().includes(q))
  }, [availableCameras, camSearch])

  const isSubmitting = form.formState.isSubmitting

  const isFilterCustomized =
    selectedEventTypes.length > 0 ||
    selectedAlgoIds.length > 0 ||
    selectedCamIds.length > 0

  return (
    <Form {...form}>
      <form
        id='alert-destination-form'
        onSubmit={form.handleSubmit(onSubmit)}
        className='space-y-4'
      >
        {/* 全局冲突或未捕获错误横幅 */}
        {conflictError && (
          <div className='flex items-start gap-2.5 rounded-xl border border-amber-500/30 bg-amber-500/10 p-3 text-xs text-amber-600 dark:text-amber-400'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{conflictError}</div>
          </div>
        )}

        {unhandledError && (
          <div className='flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='flex-1 leading-relaxed'>{unhandledError}</div>
          </div>
        )}

        {/* ========================================================= */}
        {/* 区域 1：基础信息与协议选择 */}
        {/* ========================================================= */}
        <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
          {/* 通道名称 */}
          <FormField
            control={form.control}
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

          {/* 协议类型：双列现代化单选卡片 */}
          <div className='space-y-2 pt-1'>
            <div className='flex items-center justify-between'>
              <Label className='text-xs font-semibold'>
                {t('sections.channelSelect')}
              </Label>
              {isEdit && (
                <span className='flex items-center gap-1 text-[11px] text-muted-foreground'>
                  <Lock className='size-3' />
                  {t('dialog.channelLockedNotice')}
                </span>
              )}
            </div>

            <RadioGroup
              value={channelType}
              onValueChange={(val) => handleProtocolChange(val as ChannelType)}
              disabled={isEdit}
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
                  isEdit && 'cursor-default opacity-85'
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
                    disabled={isEdit}
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
                  isEdit && 'cursor-default opacity-85'
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
                    disabled={isEdit}
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

          {/* 图片交付模式 */}
          <FormField
            control={form.control}
            name='imageMode'
            render={({ field }) => {
              const currentMode =
                field.value || (channelType === 'mqtt' ? 'base64' : 'multipart')
              const descKey = currentMode
                ? IMAGE_MODE_DESC_KEYS[currentMode]
                : undefined

              return (
                <FormItem className='space-y-1.5 rounded-lg border border-border/60 bg-muted/20 p-3'>
                  <FormLabel className='text-xs font-medium text-foreground'>
                    {t('fields.imageMode')}
                  </FormLabel>

                  <FormControl>
                    <Select
                      value={currentMode}
                      onValueChange={(val) => field.onChange(val as ImageMode)}
                    >
                      <SelectTrigger className='h-8 w-full bg-background text-xs font-medium'>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value='url'>
                          <div className='flex flex-col py-0.5 text-start'>
                            <span className='text-xs font-medium text-foreground'>
                              {t('imageModes.url')}
                            </span>
                            <span className='text-[11px] text-muted-foreground'>
                              {t('imageModes.urlDesc')}
                            </span>
                          </div>
                        </SelectItem>
                        {channelType === 'webhook' && (
                          <SelectItem value='multipart'>
                            <div className='flex flex-col py-0.5 text-start'>
                              <span className='text-xs font-medium text-foreground'>
                                {t('imageModes.multipart')}
                              </span>
                              <span className='text-[11px] text-muted-foreground'>
                                {t('imageModes.multipartDesc')}
                              </span>
                            </div>
                          </SelectItem>
                        )}
                        {channelType === 'mqtt' && (
                          <SelectItem value='base64'>
                            <div className='flex flex-col py-0.5 text-start'>
                              <span className='text-xs font-medium text-foreground'>
                                {t('imageModes.base64')}
                              </span>
                              <span className='text-[11px] text-muted-foreground'>
                                {t('imageModes.base64Desc')}
                              </span>
                            </div>
                          </SelectItem>
                        )}
                      </SelectContent>
                    </Select>
                  </FormControl>

                  {descKey && (
                    <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
                      {t(descKey)}
                    </FormDescription>
                  )}
                  <FormMessage className='text-xs' />
                </FormItem>
              )
            }}
          />
        </div>

        {/* ========================================================= */}
        {/* 区域 2：核心连接与凭据（直接呈现在主流程，无需切 Tab！） */}
        {/* ========================================================= */}
        {channelType === 'webhook' ? (
          <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
            <div className='flex items-center gap-2 border-b border-border/50 pb-2 text-xs font-semibold text-foreground'>
              <Globe className='size-3.5 text-primary' />
              <span>{t('sections.webhookEndpoint')}</span>
            </div>

            {/* Webhook URL 直接在中心展示 */}
            <FormField
              control={form.control}
              name='url'
              render={({ field }) => (
                <FormItem>
                  <FormLabel className='text-xs font-medium'>
                    {t('fields.webhookUrl')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      placeholder={t('fields.webhookUrlPlaceholder')}
                      className='font-mono text-xs'
                      autoComplete='off'
                    />
                  </FormControl>
                  <FormMessage className='text-xs' />
                </FormItem>
              )}
            />

            {/* 鉴权模式与凭据 */}
            <div className='space-y-3 pt-1'>
              <FormField
                control={form.control}
                name='authType'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className='text-xs font-medium'>
                      {t('fields.authType')}
                    </FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={(val) => field.onChange(val as AuthType)}
                    >
                      <FormControl>
                        <SelectTrigger className='h-8.5 bg-background text-xs'>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value='none'>
                          {t('authTypes.none')}
                        </SelectItem>
                        <SelectItem value='bearer'>
                          {t('authTypes.bearer')}
                        </SelectItem>
                        <SelectItem value='basic'>
                          {t('authTypes.basic')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <FormMessage className='text-xs' />
                  </FormItem>
                )}
              />

              {authType === 'bearer' && (
                <FormField
                  control={form.control}
                  name='token'
                  render={({ field }) => (
                    <FormItem className='rounded-lg border border-border/60 bg-muted/20 p-3'>
                      <FormLabel className='text-xs font-medium'>
                        {t('fields.bearerToken')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='password'
                          placeholder={
                            isEdit && destination?.secretSet
                              ? t('fields.secretRetained')
                              : t('fields.bearerTokenPlaceholder')
                          }
                          className='bg-background font-mono text-xs'
                          autoComplete='off'
                        />
                      </FormControl>
                      <FormMessage className='text-xs' />
                    </FormItem>
                  )}
                />
              )}

              {authType === 'basic' && (
                <div className='grid grid-cols-1 gap-3 rounded-lg border border-border/60 bg-muted/20 p-3 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='basicUser'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel className='text-xs font-medium'>
                          {t('fields.basicUsername')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            placeholder={
                              isEdit && destination?.secretSet
                                ? t('fields.secretRetained')
                                : t('fields.basicUsernamePlaceholder')
                            }
                            className='bg-background font-mono text-xs'
                            autoComplete='off'
                          />
                        </FormControl>
                        <FormMessage className='text-xs' />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='basicPass'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel className='text-xs font-medium'>
                          {t('fields.basicPassword')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            type='password'
                            placeholder={
                              isEdit && destination?.secretSet
                                ? t('fields.secretRetained')
                                : t('fields.basicPasswordPlaceholder')
                            }
                            className='bg-background font-mono text-xs'
                            autoComplete='off'
                          />
                        </FormControl>
                        <FormMessage className='text-xs' />
                      </FormItem>
                    )}
                  />
                </div>
              )}
            </div>

            {/* 可折叠的高级传输与标头（保持常用流程干净，不抢视线） */}
            <Collapsible
              open={advancedOpen}
              onOpenChange={setAdvancedOpen}
              className='space-y-3 rounded-lg border border-border/60 bg-muted/20 p-3 transition-colors'
            >
              <div className='flex items-center justify-between'>
                <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
                  <Sliders className='size-3.5 text-muted-foreground' />
                  <span>{t('advanced.webhookTitle')}</span>
                  <span className='text-[11px] font-normal text-muted-foreground'>
                    (
                    {t('advanced.webhookSummary', {
                      timeout: timeoutSeconds,
                      count: headers?.length || 0,
                    })}
                    )
                  </span>
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
                    <span className='sr-only'>Toggle Advanced Webhook</span>
                  </Button>
                </CollapsibleTrigger>
              </div>

              <CollapsibleContent className='space-y-3.5 pt-1'>
                {/* 超时时间 */}
                <FormField
                  control={form.control}
                  name='timeoutSeconds'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='text-xs font-medium'>
                        {t('fields.timeout')} ({t('fields.timeoutUnit')})
                      </FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={30}
                          value={field.value}
                          onChange={(e) =>
                            field.onChange(Number(e.target.value))
                          }
                          className='bg-background font-mono text-xs'
                        />
                      </FormControl>
                      <FormMessage className='text-xs' />
                    </FormItem>
                  )}
                />

                {/* 自定义标头 */}
                <div className='space-y-2.5 border-t border-border/50 pt-1'>
                  <div className='flex items-center justify-between'>
                    <Label className='text-xs font-medium text-foreground'>
                      {t('fields.customHeaders')}
                    </Label>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={handleAddHeader}
                      className='h-6.5 gap-1 px-2 text-[11px] shadow-2xs'
                    >
                      <Plus className='size-3' />
                      <span>{t('fields.addHeader')}</span>
                    </Button>
                  </div>

                  {(!headers || headers.length === 0) && (
                    <div className='flex h-12 items-center justify-center rounded-md border border-dashed border-border/70 text-xs text-muted-foreground'>
                      {t('fields.noHeaders')}
                    </div>
                  )}

                  {headers && headers.length > 0 && (
                    <div className='space-y-2'>
                      {headers.map((h, i) => {
                        const isReserved = RESERVED_HEADERS.includes(
                          h.key
                            .trim()
                            .toLowerCase() as (typeof RESERVED_HEADERS)[number]
                        )
                        return (
                          <div key={i} className='space-y-1'>
                            <div className='flex items-center gap-2'>
                              <Input
                                placeholder={t('fields.headerKeyPlaceholder')}
                                value={h.key}
                                onChange={(e) =>
                                  handleHeaderChange(i, 'key', e.target.value)
                                }
                                className={cn(
                                  'h-8 bg-background font-mono text-xs',
                                  isReserved &&
                                    'border-destructive focus-visible:ring-destructive/30'
                                )}
                              />
                              <Input
                                placeholder={t('fields.headerValuePlaceholder')}
                                value={h.value}
                                type='password'
                                onChange={(e) =>
                                  handleHeaderChange(i, 'value', e.target.value)
                                }
                                className='h-8 bg-background font-mono text-xs'
                              />
                              <Button
                                type='button'
                                variant='ghost'
                                size='icon'
                                onClick={() => handleRemoveHeader(i)}
                                className='size-8 shrink-0 text-muted-foreground hover:text-destructive'
                              >
                                <Trash2 className='size-3.5' />
                              </Button>
                            </div>
                            {isReserved && (
                              <p className='text-[11px] text-destructive'>
                                {t('fields.reservedHeaderWarning')}
                              </p>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
              </CollapsibleContent>
            </Collapsible>
          </div>
        ) : (
          /* MQTT 直观主流程 */
          <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs'>
            <div className='flex items-center gap-2 border-b border-border/50 pb-2 text-xs font-semibold text-foreground'>
              <Radio className='size-3.5 text-primary' />
              <span>{t('sections.mqttBroker')}</span>
            </div>

            <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
              <FormField
                control={form.control}
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
                control={form.control}
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
                control={form.control}
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
                control={form.control}
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
                          isEdit && destination?.secretSet
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
                  <span className='text-[11px] font-normal text-muted-foreground'>
                    (
                    {t('advanced.mqttSummary', {
                      qos,
                      keepAlive: keepAliveSeconds,
                      clientId: clientId || 'Auto ClientID',
                    })}
                    )
                  </span>
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
                    <span className='sr-only'>Toggle Advanced MQTT</span>
                  </Button>
                </CollapsibleTrigger>
              </div>

              <CollapsibleContent className='space-y-3 pt-1'>
                <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
                  <FormField
                    control={form.control}
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
                    control={form.control}
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
                            <SelectItem value='0'>0 (At most once)</SelectItem>
                            <SelectItem value='1'>1 (At least once)</SelectItem>
                            <SelectItem value='2'>2 (Exactly once)</SelectItem>
                          </SelectContent>
                        </Select>
                        <FormMessage className='text-xs' />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='keepAliveSeconds'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel className='text-xs font-medium'>
                          {t('fields.mqttKeepAlive')} (
                          {t('fields.mqttKeepAliveUnit')})
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={5}
                            max={300}
                            value={field.value}
                            onChange={(e) =>
                              field.onChange(Number(e.target.value))
                            }
                            className='bg-background font-mono text-xs'
                          />
                        </FormControl>
                        <FormMessage className='text-xs' />
                      </FormItem>
                    )}
                  />
                </div>

                <FormField
                  control={form.control}
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
        )}

        {/* ========================================================= */}
        {/* 区域 3：感知事件与数据过滤 (Filters, 可折叠收纳) */}
        {/* ========================================================= */}
        <Collapsible
          open={filtersOpen}
          onOpenChange={setFiltersOpen}
          className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs transition-colors'
        >
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
              <Filter className='size-3.5 text-primary' />
              <span>{t('advanced.filtersTitle')}</span>
              <span className='text-[11px] font-normal text-muted-foreground'>
                {isFilterCustomized
                  ? `(${t('advanced.filtersCustomSummary')})`
                  : `(${t('advanced.filtersAllSummary')})`}
              </span>
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
                    filtersOpen ? 'rotate-180' : ''
                  }`}
                />
                <span className='sr-only'>Toggle Filters</span>
              </Button>
            </CollapsibleTrigger>
          </div>

          <CollapsibleContent className='space-y-4 pt-1'>
            {/* 1. 事件类型订阅 */}
            <div className='space-y-2.5 rounded-lg border border-border/60 bg-background/60 p-3.5'>
              <div className='flex items-center justify-between border-b border-border/40 pb-2'>
                <Label className='text-xs font-semibold text-foreground'>
                  {t('sections.filterEventTypes')}
                </Label>
                <span className='text-[11px] text-muted-foreground'>
                  {selectedEventTypes.length === 0
                    ? t('eventTypes.all')
                    : t('filters.selectedCount', {
                        count: selectedEventTypes.length,
                      })}
                </span>
              </div>

              <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
                {[
                  {
                    type: 'alarm',
                    icon: ShieldAlert,
                    label: t('eventTypes.alarm'),
                    desc: t('eventTypes.alarmDesc'),
                  },
                  {
                    type: 'face_recognition',
                    icon: UserCheck,
                    label: t('eventTypes.face_recognition'),
                    desc: t('eventTypes.face_recognitionDesc'),
                  },
                  {
                    type: 'plate_recognition',
                    icon: Car,
                    label: t('eventTypes.plate_recognition'),
                    desc: t('eventTypes.plate_recognitionDesc'),
                  },
                ].map(({ type, icon: Icon, label, desc }) => {
                  const selected = selectedEventTypes.includes(type)
                  return (
                    <button
                      key={type}
                      type='button'
                      onClick={() => toggleEventType(type)}
                      className={cn(
                        'flex flex-col justify-between rounded-lg border p-2.5 text-start shadow-2xs transition-all',
                        selected
                          ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                          : 'border-border/70 bg-background hover:border-border hover:bg-muted/30'
                      )}
                    >
                      <div className='flex items-center justify-between'>
                        <div
                          className={cn(
                            'flex size-6 items-center justify-center rounded-md border text-xs',
                            selected
                              ? 'border-primary/40 bg-primary/10 text-primary'
                              : 'border-border/60 bg-muted/40 text-muted-foreground'
                          )}
                        >
                          <Icon className='size-3' />
                        </div>
                        {selected ? (
                          <div className='flex size-3.5 items-center justify-center rounded-full bg-primary text-primary-foreground'>
                            <Check className='size-2.5 stroke-[3]' />
                          </div>
                        ) : (
                          <div className='size-3.5 rounded-full border border-muted-foreground/30' />
                        )}
                      </div>
                      <div className='mt-2 space-y-0.5'>
                        <span className='text-xs font-semibold text-foreground'>
                          {label}
                        </span>
                        <p className='line-clamp-2 text-[10px] leading-relaxed text-muted-foreground'>
                          {desc}
                        </p>
                      </div>
                    </button>
                  )
                })}
              </div>
            </div>

            {/* 2. 算法过滤 */}
            <div className='space-y-2.5 rounded-lg border border-border/60 bg-background/60 p-3.5'>
              <div className='flex flex-wrap items-center justify-between gap-2 border-b border-border/40 pb-2'>
                <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
                  <Layers className='size-3.5 text-primary' />
                  <span>{t('sections.filterAlgos')}</span>
                  {selectedAlgoIds.length > 0 && (
                    <Badge
                      variant='secondary'
                      className='h-5 px-1.5 text-[10px]'
                    >
                      {selectedAlgoIds.length}
                    </Badge>
                  )}
                </div>

                <div className='flex items-center gap-2'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={handleSelectAllAlgos}
                    disabled={availableAlgorithms.length === 0}
                    className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                  >
                    {t('filters.selectAll')}
                  </Button>
                  <span className='text-border'>|</span>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={handleClearAllAlgos}
                    disabled={selectedAlgoIds.length === 0}
                    className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                  >
                    {t('filters.clearAll')}
                  </Button>
                </div>
              </div>

              {availableAlgorithms.length > 6 && (
                <div className='relative'>
                  <Search className='absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground' />
                  <Input
                    placeholder={t('filters.searchAlgoPlaceholder')}
                    value={algoSearch}
                    onChange={(e) => setAlgoSearch(e.target.value)}
                    className='h-7.5 bg-background ps-8 text-xs'
                  />
                </div>
              )}

              <div className='flex max-h-28 min-h-14 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border/60 bg-muted/20 p-2'>
                {availableAlgorithms.length === 0 ? (
                  <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
                    {t('filters.noAlgos')}
                  </div>
                ) : filteredAlgos.length === 0 ? (
                  <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
                    {t('filters.noAlgosFound')}
                  </div>
                ) : (
                  filteredAlgos.map((algo) => {
                    const selected = selectedAlgoIds.includes(algo.id)
                    return (
                      <Badge
                        key={algo.id}
                        variant={selected ? 'default' : 'outline'}
                        className='cursor-pointer text-xs font-normal transition-colors'
                        onClick={() => toggleAlgoSelection(algo.id)}
                      >
                        {algo.name}
                      </Badge>
                    )
                  })
                )}
              </div>
              <p className='text-[11px] text-muted-foreground'>
                {selectedAlgoIds.length === 0
                  ? t('filters.allAlgosActive')
                  : t('filters.selectedCount', {
                      count: selectedAlgoIds.length,
                    })}
              </p>
            </div>

            {/* 3. 摄像机过滤 */}
            <div className='space-y-2.5 rounded-lg border border-border/60 bg-background/60 p-3.5'>
              <div className='flex flex-wrap items-center justify-between gap-2 border-b border-border/40 pb-2'>
                <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
                  <Globe className='size-3.5 text-primary' />
                  <span>{t('sections.filterCameras')}</span>
                  {selectedCamIds.length > 0 && (
                    <Badge
                      variant='secondary'
                      className='h-5 px-1.5 text-[10px]'
                    >
                      {selectedCamIds.length}
                    </Badge>
                  )}
                </div>

                <div className='flex items-center gap-2'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={handleSelectAllCams}
                    disabled={availableCameras.length === 0}
                    className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                  >
                    {t('filters.selectAll')}
                  </Button>
                  <span className='text-border'>|</span>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={handleClearAllCams}
                    disabled={selectedCamIds.length === 0}
                    className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                  >
                    {t('filters.clearAll')}
                  </Button>
                </div>
              </div>

              {availableCameras.length > 6 && (
                <div className='relative'>
                  <Search className='absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground' />
                  <Input
                    placeholder={t('filters.searchCameraPlaceholder')}
                    value={camSearch}
                    onChange={(e) => setCamSearch(e.target.value)}
                    className='h-7.5 bg-background ps-8 text-xs'
                  />
                </div>
              )}

              <div className='flex max-h-28 min-h-14 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border/60 bg-muted/20 p-2'>
                {availableCameras.length === 0 ? (
                  <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
                    {t('filters.noCameras')}
                  </div>
                ) : filteredCams.length === 0 ? (
                  <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
                    {t('filters.noCamerasFound')}
                  </div>
                ) : (
                  filteredCams.map((cam) => {
                    const selected = selectedCamIds.includes(cam.id)
                    return (
                      <Badge
                        key={cam.id}
                        variant={selected ? 'default' : 'outline'}
                        className='cursor-pointer text-xs font-normal transition-colors'
                        onClick={() => toggleCamSelection(cam.id)}
                      >
                        {cam.name}
                      </Badge>
                    )
                  })
                )}
              </div>
              <p className='text-[11px] text-muted-foreground'>
                {selectedCamIds.length === 0
                  ? t('filters.allCamerasActive')
                  : t('filters.selectedCount', {
                      count: selectedCamIds.length,
                    })}
              </p>
            </div>
          </CollapsibleContent>
        </Collapsible>

        {/* ========================================================= */}
        {/* 底部 Footer：左侧连通性测试 + 右侧操作 */}
        {/* ========================================================= */}
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
            {testResult && (
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
            )}
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
              {isSubmitting && <Loader2 className='size-3.5 animate-spin' />}
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
