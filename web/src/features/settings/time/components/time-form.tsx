import { useMemo, useState } from 'react'
import { z } from 'zod'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { type TFunction } from 'i18next'
import { Clock, Laptop, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { Button } from '@/components/ui/button'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  useManualTime,
  useSyncNow,
  useSystemTime,
  useUpdateConfig,
} from '../hooks/use-system-time'
import { type SystemTimeStatus } from '../types'
import { TimezoneSelect } from './TimezoneSelect'
import { TimeStatusBanner } from './time-status-banner'

/** 校验文案必须经 `t` 解析：zod message 会原样进入 `FormMessage`。 */
function createTimeFormSchema(t?: TFunction) {
  return z.object({
    mode: z.enum(['ntp', 'manual']),
    timezone: z
      .string()
      .min(1, t ? t('validation.timezoneRequired') : '请选择时区'),
    syncIntervalSeconds: z.number().min(60).max(86400),
    ntpServers: z
      .array(
        z.object({
          value: z.string(),
        })
      )
      .min(1, t ? t('validation.ntpMin') : '至少需要 1 个 NTP 服务器')
      .max(5, t ? t('validation.ntpMax') : '最多配置 5 个 NTP 服务器'),
  })
}

type TimeFormValues = z.infer<ReturnType<typeof createTimeFormSchema>>

interface TimeFormInnerProps {
  status: SystemTimeStatus
  onRefresh: () => void
}

function TimeFormInner({ status, onRefresh }: TimeFormInnerProps) {
  const { t } = useTranslation('time')
  const updateConfig = useUpdateConfig()
  const syncNow = useSyncNow({ suppressGlobalToast: true })
  const manualTime = useManualTime({ suppressGlobalToast: true })

  const [customTime, setCustomTime] = useState<string>('')
  const [isManualSyncing, setIsManualSyncing] = useState<boolean>(false)

  const defaultServers =
    status.ntpServers && status.ntpServers.length > 0
      ? status.ntpServers.map((s) => ({ value: s }))
      : [{ value: 'ntp.aliyun.com' }, { value: 'cn.pool.ntp.org' }]

  const schema = useMemo(() => createTimeFormSchema(t), [t])

  const form = useForm<TimeFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      mode: status.mode ?? 'ntp',
      timezone: status.timezone ?? 'Asia/Shanghai',
      syncIntervalSeconds: status.syncIntervalSeconds ?? 900,
      ntpServers: defaultServers,
    },
  })

  const { fields, append, remove } = useFieldArray({
    name: 'ntpServers',
    control: form.control,
  })

  const selectedMode = useWatch({ control: form.control, name: 'mode' })

  async function ensureManualMode(): Promise<void> {
    if (form.getValues('mode') !== 'manual') {
      form.setValue('mode', 'manual')
      await updateConfig.mutateAsync({
        mode: 'manual',
        timezone: form.getValues('timezone'),
        syncIntervalSeconds: form.getValues('syncIntervalSeconds'),
        ntpServers: form
          .getValues('ntpServers')
          .map((s) => s.value.trim())
          .filter(Boolean),
      })
    }
  }

  async function onSubmit(values: TimeFormValues): Promise<void> {
    const servers = values.ntpServers.map((s) => s.value.trim()).filter(Boolean)
    if (values.mode === 'ntp' && servers.length === 0) {
      toast.error(
        t('ntp.noServersError', {
          defaultValue: '请填写至少一个有效的 NTP 服务器地址',
        })
      )
      return
    }

    try {
      await updateConfig.mutateAsync({
        mode: values.mode,
        timezone: values.timezone,
        syncIntervalSeconds: values.syncIntervalSeconds,
        ntpServers: servers.length > 0 ? servers : ['ntp.aliyun.com'],
      })
      toast.success(t('ntp.saveSuccess', { defaultValue: '对时配置已更新' }))
      onRefresh()
    } catch (err: unknown) {
      handleFormError(form, err)
    }
  }

  async function handleSyncNow(): Promise<void> {
    try {
      await toast.promise(syncNow.mutateAsync(), {
        loading: t('ntp.syncing', {
          defaultValue: '正在向上游 NTP 服务器请求高精时间校对...',
        }),
        success: t('ntp.syncSuccess', {
          defaultValue: '已成功向上游 NTP 服务器同步时间',
        }),
        error: (err) =>
          err instanceof Error
            ? err.message
            : t('errors.ntpSyncFailed', {
                defaultValue: 'NTP 服务器校时请求失败',
              }),
      })
      onRefresh()
    } catch {
      // toast.promise 已统一反馈
    }
  }

  async function handleSyncBrowserTime(): Promise<void> {
    setIsManualSyncing(true)
    try {
      const browserNow = new Date().toISOString()
      const syncPromise = (async () => {
        await manualTime.mutateAsync({ targetTime: browserNow })
        await ensureManualMode()
        return t('manual.browserSyncSuccess', {
          defaultValue: '已成功将设备时间与当前浏览器时间精准对齐并写入 RTC！',
        })
      })()

      await toast.promise(syncPromise, {
        loading: t('manual.syncingBrowser', {
          defaultValue: '正在将浏览器本地基准写入设备 RTC...',
        }),
        success: (msg) => msg,
        error: (err) =>
          err instanceof Error
            ? err.message
            : t('errors.browserSyncFailed', {
                defaultValue: '浏览器基准写入失败',
              }),
      })
      onRefresh()
    } catch {
      // toast.promise 已统一反馈
    } finally {
      setIsManualSyncing(false)
    }
  }

  async function handleApplyCustomTime(): Promise<void> {
    if (!customTime) {
      toast.warning(
        t('manual.emptyTimeWarning', {
          defaultValue: '请先选择需要设置的目标时间',
        })
      )
      return
    }

    const parsed = new Date(customTime)
    if (isNaN(parsed.getTime())) {
      toast.error(
        t('manual.invalidTimeFormat', { defaultValue: '时间格式无效' })
      )
      return
    }

    setIsManualSyncing(true)
    try {
      const applyPromise = (async () => {
        await manualTime.mutateAsync({ targetTime: parsed.toISOString() })
        await ensureManualMode()
        return t('manual.applySuccess', {
          defaultValue: '手动时钟设置成功，已固化至板载 RTC',
        })
      })()

      await toast.promise(applyPromise, {
        loading: t('manual.applyingCustom', {
          defaultValue: '正在写入自定义时间至硬件时钟...',
        }),
        success: (msg) => msg,
        error: (err) =>
          err instanceof Error
            ? err.message
            : t('errors.manualSetFailed', {
                defaultValue: '手动时间固化失败',
              }),
      })
      onRefresh()
    } catch {
      // toast.promise 已统一反馈
    } finally {
      setIsManualSyncing(false)
    }
  }

  return (
    <div className='space-y-6'>
      {/* 顶部实时状态指标面板 */}
      <TimeStatusBanner
        status={status}
        isSyncing={syncNow.isPending}
        onSyncNow={handleSyncNow}
      />

      {/* 统一规范 Shadcn Form */}
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          {/* 对时模式选择 */}
          <FormField
            control={form.control}
            name='mode'
            render={({ field }) => (
              <FormItem className='space-y-3'>
                <FormLabel>
                  {t('form.mode.label', { defaultValue: '对时模式' })}
                </FormLabel>
                <FormControl>
                  <RadioGroup
                    onValueChange={field.onChange}
                    value={field.value}
                    className='grid grid-cols-1 gap-4 sm:grid-cols-2'
                  >
                    <FormItem>
                      <FormLabel className='cursor-pointer font-normal [&:has([data-state=checked])>div]:border-primary [&:has([data-state=checked])>div]:bg-primary/5'>
                        <FormControl>
                          <RadioGroupItem value='ntp' className='sr-only' />
                        </FormControl>
                        <div className='flex flex-col gap-1 rounded-lg border border-border p-3.5 transition-colors hover:bg-muted/50'>
                          <div className='flex items-center gap-2 text-sm font-medium'>
                            <Clock className='h-4 w-4 text-primary' />
                            {t('tabs.ntp', {
                              defaultValue: 'NTP 自动网络对时',
                            })}
                          </div>
                          <p className='text-xs text-muted-foreground'>
                            {t('form.mode.ntpDesc', {
                              defaultValue:
                                '通过上游 NTP 候选服务器平滑自动校准',
                            })}
                          </p>
                        </div>
                      </FormLabel>
                    </FormItem>

                    <FormItem>
                      <FormLabel className='cursor-pointer font-normal [&:has([data-state=checked])>div]:border-primary [&:has([data-state=checked])>div]:bg-primary/5'>
                        <FormControl>
                          <RadioGroupItem value='manual' className='sr-only' />
                        </FormControl>
                        <div className='flex flex-col gap-1 rounded-lg border border-border p-3.5 transition-colors hover:bg-muted/50'>
                          <div className='flex items-center gap-2 text-sm font-medium'>
                            <Laptop className='h-4 w-4 text-primary' />
                            {t('tabs.manual', {
                              defaultValue: '手动 / 浏览器对时',
                            })}
                          </div>
                          <p className='text-xs text-muted-foreground'>
                            {t('form.mode.manualDesc', {
                              defaultValue: '现场一键同步客户端时间或手动设定',
                            })}
                          </p>
                        </div>
                      </FormLabel>
                    </FormItem>
                  </RadioGroup>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          {/* 系统物理时区选择 */}
          <FormField
            control={form.control}
            name='timezone'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('ntp.timezoneLabel', {
                    defaultValue: '系统物理时区 (Timezone)',
                  })}
                </FormLabel>
                <FormControl>
                  <TimezoneSelect
                    value={field.value}
                    onChange={field.onChange}
                  />
                </FormControl>
                <FormDescription>
                  {t('form.timezone.desc', {
                    defaultValue:
                      '物理联动修改宿主机 /etc/localtime 软链接，并热刷新 Go/C 运行时时区缓存。',
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {/* NTP 模式特有表单项 */}
          {selectedMode === 'ntp' && (
            <div className='space-y-6 pt-2'>
              {/* 同步轮询周期 */}
              <FormField
                control={form.control}
                name='syncIntervalSeconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('ntp.intervalLabel', {
                        defaultValue: '自动同步轮询周期',
                      })}
                    </FormLabel>
                    <Select
                      value={String(field.value)}
                      onValueChange={(val) => field.onChange(Number(val))}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue
                            placeholder={t('interval.placeholder')}
                          />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value='300'>{t('interval.m5')}</SelectItem>
                        <SelectItem value='900'>{t('interval.m15')}</SelectItem>
                        <SelectItem value='1800'>
                          {t('interval.m30')}
                        </SelectItem>
                        <SelectItem value='3600'>{t('interval.h1')}</SelectItem>
                        <SelectItem value='86400'>
                          {t('interval.h24')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t('ntp.desc', {
                        defaultValue:
                          '后台服务定期向上游 NTP 源发起高精度时间探测并执行平滑微调。',
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* NTP 服务器候选池 */}
              <div className='space-y-3'>
                <div>
                  <FormLabel>
                    {t('ntp.serversLabel', {
                      defaultValue: 'NTP 服务器候选池 (按序降级探测)',
                    })}
                  </FormLabel>
                  <FormDescription>
                    {t('ntp.desc', {
                      defaultValue:
                        '按序降级探测，主源超时（3s）或异常时自动切换备用源。',
                    })}
                  </FormDescription>
                </div>

                <div className='space-y-2'>
                  {fields.map((item, index) => (
                    <FormField
                      key={item.id}
                      control={form.control}
                      name={`ntpServers.${index}.value`}
                      render={({ field }) => (
                        <FormItem>
                          <div className='flex items-center gap-2'>
                            <FormControl>
                              <Input
                                placeholder={`ntp-server-${index + 1}.example.com`}
                                {...field}
                              />
                            </FormControl>
                            <Button
                              type='button'
                              variant='outline'
                              size='icon'
                              disabled={fields.length <= 1}
                              onClick={() => remove(index)}
                              className='shrink-0'
                            >
                              <Trash2 className='h-4 w-4' />
                            </Button>
                          </div>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  ))}
                </div>

                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={fields.length >= 5}
                  onClick={() => append({ value: '' })}
                  className='gap-1.5'
                >
                  <Plus className='h-3.5 w-3.5' />
                  {t('ntp.addServer', { defaultValue: '添加服务器' })}
                </Button>
              </div>
            </div>
          )}

          {/* 手动模式特有表单项 */}
          {selectedMode === 'manual' && (
            <div className='space-y-4 pt-2'>
              {/* 一键同步浏览器时间操作横幅 */}
              <div className='flex flex-col items-start justify-between gap-4 rounded-lg border border-primary/20 bg-primary/5 p-4 sm:flex-row sm:items-center'>
                <div className='space-y-1'>
                  <div className='flex items-center gap-2 text-sm font-semibold text-primary'>
                    <Laptop className='h-4 w-4' />
                    {t('manual.browserSyncTitle', {
                      defaultValue: '一键同步浏览器时间 (离线推荐)',
                    })}
                  </div>
                  <p className='text-xs text-muted-foreground'>
                    {t('manual.browserSyncDesc', {
                      defaultValue:
                        '现场工程人员电脑直连边缘设备时，以毫秒级精度同步当前客户端时间并写入硬件 RTC。',
                    })}
                  </p>
                </div>
                <Button
                  type='button'
                  variant='default'
                  size='sm'
                  onClick={handleSyncBrowserTime}
                  disabled={isManualSyncing}
                  className='shrink-0 gap-1.5'
                >
                  <Clock className='h-3.5 w-3.5' />
                  {t('manual.syncBrowserBtn', {
                    defaultValue: '立即同步浏览器时间',
                  })}
                </Button>
              </div>

              {/* 手动输入指定时间 */}
              <div className='space-y-2 rounded-lg border p-4'>
                <FormLabel className='text-sm font-medium'>
                  {t('manual.customTimeLabel', {
                    defaultValue: '手动输入指定时间',
                  })}
                </FormLabel>
                <div className='flex flex-wrap items-center gap-2'>
                  <Input
                    type='datetime-local'
                    value={customTime}
                    onChange={(e) => setCustomTime(e.target.value)}
                    className='max-w-xs'
                  />
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={handleApplyCustomTime}
                    disabled={isManualSyncing || !customTime}
                  >
                    {t('manual.applyCustomBtn', {
                      defaultValue: '应用指定时间',
                    })}
                  </Button>
                </div>
                <p className='text-xs text-muted-foreground'>
                  {t('manual.desc', {
                    defaultValue:
                      '手动设定的时间将无视 Panic 门限以 Step 阶跃方式直接写入系统内核并回写 RTC。',
                  })}
                </p>
              </div>
            </div>
          )}

          {/* 底部保存提交操作 */}
          <div className='pt-2'>
            <Button type='submit' disabled={updateConfig.isPending}>
              {updateConfig.isPending && (
                <RefreshCw className='mr-2 h-4 w-4 animate-spin' />
              )}
              {t('form.submit', { defaultValue: '保存对时配置' })}
            </Button>
          </div>
        </form>
      </Form>
    </div>
  )
}

export function TimeForm() {
  const { data: status, isLoading, refetch } = useSystemTime()

  if (isLoading || !status) {
    return (
      <div className='space-y-6'>
        <Skeleton className='h-28 w-full rounded-lg' />
        <Skeleton className='h-12 w-full rounded-lg' />
        <Skeleton className='h-10 w-full' />
        <Skeleton className='h-10 w-full' />
      </div>
    )
  }

  return (
    <TimeFormInner
      key={`${status.mode}-${status.timezone}-${status.syncIntervalSeconds}-${status.ntpServers?.join(',')}`}
      status={status}
      onRefresh={() => void refetch()}
    />
  )
}
