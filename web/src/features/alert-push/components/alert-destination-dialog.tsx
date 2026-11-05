import { useEffect, useState } from 'react'
import {
  Globe,
  Radio,
  Plus,
  Trash2,
  Play,
  Loader2,
  CheckCircle2,
  AlertCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { algorithmsApi } from '@/features/algorithms/api/algorithms-api'
import { camerasApi } from '@/features/cameras/api/cameras-api'
import { alertPushApi } from '../api/alert-push-api'
import type {
  AlertDestination,
  AuthType,
  ChannelType,
  CreateAlertDestinationInput,
  ImageMode,
  TestDestinationResult,
  UpdateAlertDestinationInput,
} from '../types'

const RESERVED_HEADERS = [
  'authorization',
  'content-type',
  'content-length',
  'user-agent',
  'host',
  'accept',
  'connection',
]

interface HeaderEntry {
  key: string
  value: string
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
      <DialogContent className='max-h-[85vh] max-w-2xl overflow-y-auto'>
        <DialogHeader>
          <DialogTitle>{isEdit ? t('edit') : t('create')}</DialogTitle>
          <DialogDescription>{t('description')}</DialogDescription>
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

  // Initial State derived from destination
  const [name, setName] = useState(destination?.name || '')
  const [channelType, setChannelType] = useState<ChannelType>(
    destination?.channelType || 'webhook'
  )
  const [imageMode, setImageMode] = useState<ImageMode>(
    destination?.imageMode ||
      (destination?.channelType === 'mqtt' ? 'base64' : 'multipart')
  )

  // Webhook
  const [url, setUrl] = useState(destination?.config?.url || '')
  const [authType, setAuthType] = useState<AuthType>(
    destination?.config?.authType || 'none'
  )
  const [token, setToken] = useState('')
  const [basicUser, setBasicUser] = useState('')
  const [basicPass, setBasicPass] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(
    destination?.config?.timeoutSeconds || 5
  )
  const [headers, setHeaders] = useState<HeaderEntry[]>(() =>
    (destination?.headerNames || []).map((k) => ({ key: k, value: '' }))
  )

  // MQTT
  const [broker, setBroker] = useState(destination?.config?.broker || 'tcp://')
  const [clientId, setClientId] = useState(destination?.config?.clientId || '')
  const [mqttUser, setMqttUser] = useState(destination?.config?.username || '')
  const [mqttPass, setMqttPass] = useState('')
  const [topic, setTopic] = useState(
    destination?.config?.topic || 'zhulong/alarms'
  )
  const [qos, setQos] = useState(destination?.config?.qos ?? 0)
  const [keepAlive, setKeepAlive] = useState(
    destination?.config?.keepAliveSeconds || 30
  )
  const [tlsSkipVerify, setTlsSkipVerify] = useState(
    destination?.config?.tlsSkipVerify || false
  )

  // Filter
  const [availableAlgorithms, setAvailableAlgorithms] = useState<
    Array<{ id: string; name: string }>
  >([])
  const [availableCameras, setAvailableCameras] = useState<
    Array<{ id: string; name: string }>
  >([])
  const [selectedEventTypes, setSelectedEventTypes] = useState<string[]>(
    destination?.filter?.eventTypes || []
  )
  const [selectedAlgoIds, setSelectedAlgoIds] = useState<string[]>(
    destination?.filter?.algorithmIds || []
  )
  const [selectedCamIds, setSelectedCamIds] = useState<string[]>(
    destination?.filter?.cameraIds || []
  )

  const toggleEventType = (type: string) => {
    setSelectedEventTypes((prev) =>
      prev.includes(type) ? prev.filter((t) => t !== type) : [...prev, type]
    )
  }

  // Action status
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<TestDestinationResult | null>(
    null
  )
  const [submitting, setSubmitting] = useState(false)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

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

  const handleChannelTypeChange = (ct: ChannelType) => {
    setChannelType(ct)
    if (ct === 'webhook') {
      if (imageMode === 'base64') setImageMode('multipart')
    } else {
      if (imageMode === 'multipart') setImageMode('base64')
    }
  }

  const handleAddHeader = () => {
    setHeaders([...headers, { key: '', value: '' }])
  }

  const handleRemoveHeader = (idx: number) => {
    setHeaders(headers.filter((_, i) => i !== idx))
  }

  const handleHeaderChange = (
    idx: number,
    field: 'key' | 'value',
    val: string
  ) => {
    const updated = [...headers]
    updated[idx][field] = val
    setHeaders(updated)
  }

  const handleRunTest = async () => {
    setTesting(true)
    setTestResult(null)
    setErrorMessage(null)

    const hdrsObj: Record<string, string> = {}
    for (const h of headers) {
      if (h.key.trim() && h.value) {
        hdrsObj[h.key.trim()] = h.value
      }
    }

    try {
      const res = await alertPushApi.test({
        id: destination?.id,
        channelType,
        imageMode,
        config:
          channelType === 'webhook'
            ? { url, authType, timeoutSeconds }
            : {
                broker,
                clientId,
                username: mqttUser,
                topic,
                qos,
                keepAliveSeconds: keepAlive,
                tlsSkipVerify,
              },
        secret:
          channelType === 'webhook'
            ? {
                token: token ? token : undefined,
                username: basicUser ? basicUser : undefined,
                password: basicPass ? basicPass : undefined,
                headers: Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
              }
            : {
                password: mqttPass ? mqttPass : undefined,
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

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setErrorMessage(null)

    for (const h of headers) {
      if (RESERVED_HEADERS.includes(h.key.trim().toLowerCase())) {
        setErrorMessage(
          `Header "${h.key}" is reserved and cannot be customized`
        )
        setSubmitting(false)
        return
      }
    }

    if (
      channelType === 'mqtt' &&
      (topic.includes('+') || topic.includes('#'))
    ) {
      setErrorMessage('MQTT publish topic must not contain wildcards (+ or #)')
      setSubmitting(false)
      return
    }

    const hdrsObj: Record<string, string> = {}
    for (const h of headers) {
      if (h.key.trim() && h.value) {
        hdrsObj[h.key.trim()] = h.value
      }
    }

    try {
      if (isEdit && destination) {
        const updatePayload: UpdateAlertDestinationInput = {
          name: name.trim(),
          imageMode,
          filter: {
            eventTypes: selectedEventTypes,
            algorithmIds: selectedAlgoIds,
            cameraIds: selectedCamIds,
          },
          config:
            channelType === 'webhook'
              ? { url: url.trim(), authType, timeoutSeconds }
              : {
                  broker: broker.trim(),
                  clientId: clientId.trim(),
                  username: mqttUser.trim(),
                  topic: topic.trim(),
                  qos,
                  keepAliveSeconds: keepAlive,
                  tlsSkipVerify,
                },
          secret:
            channelType === 'webhook'
              ? {
                  token: token ? token : undefined,
                  username: basicUser ? basicUser : undefined,
                  password: basicPass ? basicPass : undefined,
                  headers:
                    Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
                }
              : {
                  password: mqttPass ? mqttPass : undefined,
                },
        }
        await alertPushApi.update(destination.id, updatePayload)
      } else {
        const createPayload: CreateAlertDestinationInput = {
          name: name.trim(),
          channelType,
          imageMode,
          filter: {
            eventTypes: selectedEventTypes,
            algorithmIds: selectedAlgoIds,
            cameraIds: selectedCamIds,
          },
          config:
            channelType === 'webhook'
              ? { url: url.trim(), authType, timeoutSeconds }
              : {
                  broker: broker.trim(),
                  clientId: clientId.trim(),
                  username: mqttUser.trim(),
                  topic: topic.trim(),
                  qos,
                  keepAliveSeconds: keepAlive,
                  tlsSkipVerify,
                },
          secret:
            channelType === 'webhook'
              ? {
                  token: token ? token : undefined,
                  username: basicUser ? basicUser : undefined,
                  password: basicPass ? basicPass : undefined,
                  headers:
                    Object.keys(hdrsObj).length > 0 ? hdrsObj : undefined,
                }
              : {
                  password: mqttPass ? mqttPass : undefined,
                },
        }
        await alertPushApi.create(createPayload)
      }
      onSuccess()
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  const toggleAlgoSelection = (id: string) => {
    setSelectedAlgoIds((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
    )
  }

  const toggleCamSelection = (id: string) => {
    setSelectedCamIds((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
    )
  }

  return (
    <form onSubmit={handleSubmit}>
      {errorMessage && (
        <div className='my-3 flex items-center gap-2 rounded-md bg-destructive/15 p-3 text-sm text-destructive'>
          <AlertCircle className='size-4 shrink-0' />
          <span>{errorMessage}</span>
        </div>
      )}

      <div className='grid gap-4 py-4'>
        {/* General Info */}
        <div className='grid grid-cols-2 gap-4'>
          <div className='space-y-1.5'>
            <Label htmlFor='dest-name'>{t('fields.name')}</Label>
            <Input
              id='dest-name'
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder='e.g. SOC Webhook'
              required
            />
          </div>

          <div className='space-y-1.5'>
            <Label>{t('fields.channelType')}</Label>
            <Select
              value={channelType}
              onValueChange={(val) =>
                handleChannelTypeChange(val as ChannelType)
              }
              disabled={isEdit}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='webhook'>
                  <div className='flex items-center gap-2'>
                    <Globe className='size-4 text-sky-500' />
                    <span>{t('channelTypes.webhook')}</span>
                  </div>
                </SelectItem>
                <SelectItem value='mqtt'>
                  <div className='flex items-center gap-2'>
                    <Radio className='size-4 text-violet-500' />
                    <span>{t('channelTypes.mqtt')}</span>
                  </div>
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className='space-y-1.5'>
          <Label>{t('fields.imageMode')}</Label>
          <Select
            value={imageMode}
            onValueChange={(val) => setImageMode(val as ImageMode)}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='url'>{t('imageModes.url')}</SelectItem>
              {channelType === 'webhook' && (
                <SelectItem value='multipart'>
                  {t('imageModes.multipart')}
                </SelectItem>
              )}
              {channelType === 'mqtt' && (
                <SelectItem value='base64'>{t('imageModes.base64')}</SelectItem>
              )}
            </SelectContent>
          </Select>
        </div>

        {/* Protocol Configuration */}
        {channelType === 'webhook' ? (
          <div className='space-y-4 rounded-lg border bg-muted/20 p-4'>
            <div className='space-y-1.5'>
              <Label htmlFor='webhook-url'>{t('fields.webhookUrl')}</Label>
              <Input
                id='webhook-url'
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder='https://api.example.com/alerts'
                required
              />
            </div>

            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-1.5'>
                <Label>{t('fields.authType')}</Label>
                <Select
                  value={authType}
                  onValueChange={(val) => setAuthType(val as AuthType)}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='none'>{t('authTypes.none')}</SelectItem>
                    <SelectItem value='bearer'>
                      {t('authTypes.bearer')}
                    </SelectItem>
                    <SelectItem value='basic'>
                      {t('authTypes.basic')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div className='space-y-1.5'>
                <Label htmlFor='webhook-timeout'>{t('fields.timeout')}</Label>
                <Input
                  id='webhook-timeout'
                  type='number'
                  min={1}
                  max={30}
                  value={timeoutSeconds}
                  onChange={(e) => setTimeoutSeconds(Number(e.target.value))}
                />
              </div>
            </div>

            {authType === 'bearer' && (
              <div className='space-y-1.5'>
                <Label htmlFor='webhook-token'>{t('fields.bearerToken')}</Label>
                <Input
                  id='webhook-token'
                  type='password'
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder={
                    isEdit && destination?.secretSet
                      ? t('fields.secretRetained')
                      : 'Bearer token value'
                  }
                  required={!isEdit || !destination?.secretSet}
                />
              </div>
            )}

            {authType === 'basic' && (
              <div className='grid grid-cols-2 gap-4'>
                <div className='space-y-1.5'>
                  <Label htmlFor='basic-user'>
                    {t('fields.basicUsername')}
                  </Label>
                  <Input
                    id='basic-user'
                    value={basicUser}
                    onChange={(e) => setBasicUser(e.target.value)}
                    placeholder={
                      isEdit && destination?.secretSet
                        ? t('fields.secretRetained')
                        : 'username'
                    }
                    required={!isEdit || !destination?.secretSet}
                  />
                </div>
                <div className='space-y-1.5'>
                  <Label htmlFor='basic-pass'>
                    {t('fields.basicPassword')}
                  </Label>
                  <Input
                    id='basic-pass'
                    type='password'
                    value={basicPass}
                    onChange={(e) => setBasicPass(e.target.value)}
                    placeholder={
                      isEdit && destination?.secretSet
                        ? t('fields.secretRetained')
                        : 'password'
                    }
                    required={!isEdit || !destination?.secretSet}
                  />
                </div>
              </div>
            )}

            {/* Custom Headers */}
            <div className='space-y-2 pt-2'>
              <div className='flex items-center justify-between'>
                <Label className='text-xs font-semibold'>
                  {t('fields.customHeaders')}
                </Label>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={handleAddHeader}
                  className='h-7 gap-1 text-xs'
                >
                  <Plus className='size-3' />
                  {t('fields.addHeader')}
                </Button>
              </div>

              {headers.map((h, i) => (
                <div key={i} className='flex items-center gap-2'>
                  <Input
                    placeholder={t('fields.headerKey')}
                    value={h.key}
                    onChange={(e) =>
                      handleHeaderChange(i, 'key', e.target.value)
                    }
                    className='h-8 font-mono text-xs'
                  />
                  <Input
                    placeholder={t('fields.headerValue')}
                    value={h.value}
                    type='password'
                    onChange={(e) =>
                      handleHeaderChange(i, 'value', e.target.value)
                    }
                    className='h-8 font-mono text-xs'
                  />
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    onClick={() => handleRemoveHeader(i)}
                    className='size-8 text-destructive'
                  >
                    <Trash2 className='size-3.5' />
                  </Button>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className='space-y-4 rounded-lg border bg-muted/20 p-4'>
            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-broker'>{t('fields.mqttBroker')}</Label>
                <Input
                  id='mqtt-broker'
                  value={broker}
                  onChange={(e) => setBroker(e.target.value)}
                  placeholder='tcp://10.0.0.1:1883'
                  required
                />
              </div>

              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-topic'>{t('fields.mqttTopic')}</Label>
                <Input
                  id='mqtt-topic'
                  value={topic}
                  onChange={(e) => setTopic(e.target.value)}
                  placeholder='zhulong/alarms'
                  required
                />
              </div>
            </div>

            <div className='grid grid-cols-3 gap-4'>
              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-client-id'>
                  {t('fields.mqttClientId')}
                </Label>
                <Input
                  id='mqtt-client-id'
                  value={clientId}
                  maxLength={23}
                  onChange={(e) => setClientId(e.target.value)}
                  placeholder='Auto derived (z...)'
                />
              </div>

              <div className='space-y-1.5'>
                <Label>{t('fields.mqttQoS')}</Label>
                <Select
                  value={String(qos)}
                  onValueChange={(val) => setQos(Number(val))}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='0'>0 (At most once)</SelectItem>
                    <SelectItem value='1'>1 (At least once)</SelectItem>
                    <SelectItem value='2'>2 (Exactly once)</SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-keepalive'>
                  {t('fields.mqttKeepAlive')}
                </Label>
                <Input
                  id='mqtt-keepalive'
                  type='number'
                  min={5}
                  value={keepAlive}
                  onChange={(e) => setKeepAlive(Number(e.target.value))}
                />
              </div>
            </div>

            <div className='grid grid-cols-2 gap-4'>
              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-user'>{t('fields.mqttUsername')}</Label>
                <Input
                  id='mqtt-user'
                  value={mqttUser}
                  onChange={(e) => setMqttUser(e.target.value)}
                />
              </div>

              <div className='space-y-1.5'>
                <Label htmlFor='mqtt-pass'>{t('fields.mqttPassword')}</Label>
                <Input
                  id='mqtt-pass'
                  type='password'
                  value={mqttPass}
                  onChange={(e) => setMqttPass(e.target.value)}
                  placeholder={
                    isEdit && destination?.secretSet
                      ? t('fields.secretRetained')
                      : ''
                  }
                />
              </div>
            </div>

            <div className='flex items-center space-x-2 pt-2'>
              <Switch
                id='tls-skip-verify'
                checked={tlsSkipVerify}
                onCheckedChange={setTlsSkipVerify}
              />
              <Label
                htmlFor='tls-skip-verify'
                className='cursor-pointer text-sm'
              >
                {t('fields.tlsSkipVerify')}
              </Label>
            </div>
          </div>
        )}

        {/* Filter Rules */}
        <div className='space-y-3 rounded-lg border bg-muted/10 p-4'>
          <div className='space-y-1.5'>
            <div className='flex items-center justify-between'>
              <Label className='text-xs font-semibold'>
                {t('fields.filterEvents')}
              </Label>
              {selectedEventTypes.length === 0 && (
                <span className='text-[11px] text-muted-foreground'>
                  ({t('eventTypes.all')})
                </span>
              )}
            </div>
            <div className='flex flex-wrap gap-1.5'>
              {(
                ['alarm', 'face_recognition', 'plate_recognition'] as const
              ).map((eventType) => {
                const selected = selectedEventTypes.includes(eventType)
                return (
                  <Badge
                    key={eventType}
                    variant={selected ? 'default' : 'outline'}
                    className='cursor-pointer text-xs'
                    onClick={() => toggleEventType(eventType)}
                  >
                    {t(`eventTypes.${eventType}`)}
                  </Badge>
                )
              })}
            </div>
          </div>

          <div className='space-y-1.5'>
            <Label className='text-xs font-semibold'>
              {t('fields.filterAlgorithms')}
            </Label>
            <div className='flex max-h-24 flex-wrap gap-1.5 overflow-y-auto rounded-md border bg-background p-1.5'>
              {availableAlgorithms.map((algo) => {
                const selected = selectedAlgoIds.includes(algo.id)
                return (
                  <Badge
                    key={algo.id}
                    variant={selected ? 'default' : 'outline'}
                    className='cursor-pointer text-xs'
                    onClick={() => toggleAlgoSelection(algo.id)}
                  >
                    {algo.name}
                  </Badge>
                )
              })}
              {availableAlgorithms.length === 0 && (
                <span className='p-1 text-xs text-muted-foreground'>
                  No installed algorithms
                </span>
              )}
            </div>
          </div>

          <div className='space-y-1.5'>
            <Label className='text-xs font-semibold'>
              {t('fields.filterCameras')}
            </Label>
            <div className='flex max-h-24 flex-wrap gap-1.5 overflow-y-auto rounded-md border bg-background p-1.5'>
              {availableCameras.map((cam) => {
                const selected = selectedCamIds.includes(cam.id)
                return (
                  <Badge
                    key={cam.id}
                    variant={selected ? 'default' : 'outline'}
                    className='cursor-pointer text-xs'
                    onClick={() => toggleCamSelection(cam.id)}
                  >
                    {cam.name}
                  </Badge>
                )
              })}
              {availableCameras.length === 0 && (
                <span className='p-1 text-xs text-muted-foreground'>
                  No configured cameras
                </span>
              )}
            </div>
          </div>
        </div>

        {/* Test Result Indicator */}
        {testResult && (
          <div
            className={`flex items-center gap-2 rounded-md p-3 text-sm ${
              testResult.ok
                ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
                : 'bg-destructive/15 text-destructive'
            }`}
          >
            {testResult.ok ? (
              <CheckCircle2 className='size-4 shrink-0' />
            ) : (
              <AlertCircle className='size-4 shrink-0' />
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
          </div>
        )}
      </div>

      <DialogFooter className='flex items-center justify-between sm:justify-between'>
        <Button
          type='button'
          variant='outline'
          disabled={testing || submitting}
          onClick={handleRunTest}
          className='gap-1.5'
        >
          {testing ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <Play className='size-4' />
          )}
          {testing ? t('testing') : t('test')}
        </Button>

        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='ghost'
            onClick={onCancel}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button type='submit' disabled={submitting}>
            {submitting && <Loader2 className='mr-1.5 size-4 animate-spin' />}
            Save
          </Button>
        </div>
      </DialogFooter>
    </form>
  )
}
