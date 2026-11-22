import { useEffect, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  FolderSearch,
  CheckCircle2,
  AlertCircle,
  ChevronDown,
  Sliders,
  Save,
  RotateCcw,
  Loader2,
  HardDrive,
  Cloud,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { handleFormError } from '@/lib/form-error'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
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
import {
  useTestStorageConnection,
  useTestStoragePath,
  useUpdateStorageConfig,
} from '../hooks/use-storage'
import {
  type ConnectionTestResponse,
  type PathTestResponse,
  type StorageConfig,
  type StorageStatus,
} from '../types'
import { formatBytes } from '../utils/format'
import { S3BackendFields } from './s3-backend-fields'
import {
  configToFormValues,
  createStorageFormSchema,
  formValuesToUpdatePayload,
  type StorageFormValues,
} from './storage-form-schema'

interface StorageConfigFormProps {
  initialConfig: StorageConfig
  status?: StorageStatus | null
  onConfigUpdated?: () => void
}

export function StorageConfigForm({
  initialConfig,
  onConfigUpdated,
}: StorageConfigFormProps) {
  const { t } = useTranslation('storage')
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [testResult, setTestResult] = useState<PathTestResponse | null>(null)
  const [connectionResult, setConnectionResult] =
    useState<ConnectionTestResponse | null>(null)

  const updateMutation = useUpdateStorageConfig()
  const testMutation = useTestStoragePath({ suppressGlobalToast: true })
  const connectionMutation = useTestStorageConnection({
    suppressGlobalToast: true,
  })

  const schema = useMemo(() => createStorageFormSchema(t), [t])

  const form = useForm<StorageFormValues>({
    resolver: zodResolver(schema),
    defaultValues: configToFormValues(initialConfig),
  })

  useEffect(() => {
    form.reset(configToFormValues(initialConfig))
  }, [form, initialConfig])

  const currentBackend = useWatch({
    control: form.control,
    name: 'backend',
  })
  const currentPath = useWatch({
    control: form.control,
    name: 'mediaDirectory',
  })

  const handleApplyPreset = (preset: 'minio' | 'cloud') => {
    if (preset === 'minio') {
      form.setValue('s3.usePathStyle', true, { shouldDirty: true })
      form.setValue('s3.useTls', false, { shouldDirty: true })
    } else {
      form.setValue('s3.usePathStyle', false, { shouldDirty: true })
      form.setValue('s3.useTls', true, { shouldDirty: true })
    }
    setConnectionResult(null)
  }

  const handleTestPath = async () => {
    if (!currentPath) {
      toast.error(t('form.empty_path', { defaultValue: '请先输入目录路径' }))
      return
    }

    const testPromise = testMutation
      .mutateAsync({ path: currentPath })
      .then((res) => {
        setTestResult(res)
        if (res.writable) {
          return t('form.test_success', {
            defaultValue: '路径检测通过，具备读写权能！',
          })
        }
        throw new Error(
          res.errorReason ||
            t('form.test_failed', {
              defaultValue: '该目录不存在或无写权限',
            })
        )
      })

    try {
      await toast.promise(testPromise, {
        loading: t('form.testing_path', {
          defaultValue: '正在探测目录读写权限与可用空间...',
        }),
        success: (msg) => msg,
        error: (err) =>
          err instanceof Error
            ? err.message
            : t('form.test_failed', {
                defaultValue: '该目录不存在或无写权限',
              }),
      })
    } catch {
      // toast.promise 已统一反馈
    }
  }

  const handleTestConnection = async () => {
    const valid = await form.trigger([
      's3.endpoint',
      's3.bucket',
      's3.accessKeyId',
    ])
    if (!valid) {
      return
    }
    const s3 = form.getValues('s3')
    const probePromise = connectionMutation
      .mutateAsync({
        backend: 's3',
        s3: {
          endpoint: s3.endpoint,
          region: s3.region,
          bucket: s3.bucket,
          prefix: s3.prefix,
          usePathStyle: s3.usePathStyle,
          useTls: s3.useTls,
          insecureSkipVerify: s3.insecureSkipVerify,
          accessKeyId: s3.accessKeyId,
          secretAccessKey: s3.secretAccessKey.trim() || undefined,
          hasSecretAccessKey: s3.hasSecretAccessKey,
          quotaGb: s3.quotaGb,
        },
      })
      .then((res) => {
        setConnectionResult(res)
        if (res.writable) {
          return t('s3.conn_success', {
            defaultValue: 'S3 / MinIO 连通性与写入探测通过！',
          })
        }
        throw new Error(
          res.errorReason ||
            t('s3.conn_failed', {
              defaultValue: '无法连接目标桶或写入探测失败',
            })
        )
      })

    try {
      await toast.promise(probePromise, {
        loading: t('s3.testing_connection', {
          defaultValue: '正在探测 S3 桶连通性与写入权限...',
        }),
        success: (msg) => msg,
        error: (err) =>
          err instanceof Error
            ? err.message
            : t('s3.conn_failed', {
                defaultValue: '无法连接目标桶或写入探测失败',
              }),
      })
    } catch {
      // toast.promise 已统一反馈
    }
  }

  const onSubmit = async (values: StorageFormValues) => {
    try {
      const payload = formValuesToUpdatePayload(values, initialConfig)
      await updateMutation.mutateAsync(payload)
      toast.success(
        t('form.save_success', { defaultValue: '存储策略配置已保存并生效！' })
      )
      if (onConfigUpdated) {
        onConfigUpdated()
      }
    } catch (err: unknown) {
      handleFormError(form, err)
    }
  }

  return (
    <Card className='border-border/60 bg-card/60 backdrop-blur-sm'>
      <CardHeader>
        <CardTitle className='text-base font-semibold'>
          {t('form.title', { defaultValue: '媒体存储路径与保留策略配置' })}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
            {/* Backend Provider Switcher */}
            <FormField
              control={form.control}
              name='backend'
              render={({ field }) => (
                <FormItem className='space-y-2'>
                  <FormLabel className='font-medium'>
                    {t('backend.label', { defaultValue: '存储后端驱动' })}
                  </FormLabel>
                  <FormControl>
                    <div
                      role='radiogroup'
                      aria-label={t('backend.label', {
                        defaultValue: '存储后端驱动',
                      })}
                      className='grid grid-cols-1 gap-3 sm:grid-cols-2'
                    >
                      <button
                        type='button'
                        role='radio'
                        aria-checked={field.value === 'local'}
                        onClick={() => {
                          field.onChange('local')
                          setConnectionResult(null)
                        }}
                        className={cn(
                          'flex items-start gap-3 rounded-lg border p-3.5 text-left transition-colors',
                          field.value === 'local'
                            ? 'border-primary bg-primary/5'
                            : 'border-border/60 hover:bg-muted/40'
                        )}
                      >
                        <HardDrive className='mt-0.5 size-4 shrink-0 text-primary' />
                        <div className='space-y-0.5'>
                          <div className='text-sm font-medium'>
                            {t('backend.local_title', {
                              defaultValue: '本地文件系统 (LocalFS)',
                            })}
                          </div>
                          <p className='text-xs text-muted-foreground'>
                            {t('backend.local_desc')}
                          </p>
                        </div>
                      </button>

                      <button
                        type='button'
                        role='radio'
                        aria-checked={field.value === 's3'}
                        onClick={() => {
                          field.onChange('s3')
                          setTestResult(null)
                        }}
                        className={cn(
                          'flex items-start gap-3 rounded-lg border p-3.5 text-left transition-colors',
                          field.value === 's3'
                            ? 'border-primary bg-primary/5'
                            : 'border-border/60 hover:bg-muted/40'
                        )}
                      >
                        <Cloud className='mt-0.5 size-4 shrink-0 text-primary' />
                        <div className='space-y-0.5'>
                          <div className='text-sm font-medium'>
                            {t('backend.s3_title', {
                              defaultValue: 'MinIO / 云对象存储 (S3)',
                            })}
                          </div>
                          <p className='text-xs text-muted-foreground'>
                            {t('backend.s3_desc')}
                          </p>
                        </div>
                      </button>
                    </div>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {/* LocalFS Path Settings */}
            {currentBackend === 'local' ? (
              <div className='space-y-3'>
                <FormField
                  control={form.control}
                  name='mediaDirectory'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='font-medium'>
                        {t('form.media_directory', {
                          defaultValue: '媒体根目录绝对路径',
                        })}
                      </FormLabel>
                      <div className='flex gap-2'>
                        <FormControl>
                          <Input
                            placeholder={t('form.mediaDirectoryPlaceholder')}
                            className='font-mono text-sm'
                            {...field}
                            onChange={(e) => {
                              field.onChange(e)
                              setTestResult(null)
                            }}
                          />
                        </FormControl>
                        <Button
                          type='button'
                          variant='secondary'
                          onClick={handleTestPath}
                          disabled={testMutation.isPending}
                          className='shrink-0 gap-1.5'
                        >
                          {testMutation.isPending ? (
                            <Loader2 className='size-3.5 animate-spin' />
                          ) : (
                            <FolderSearch className='size-3.5' />
                          )}
                          {t('form.btn_test_path', {
                            defaultValue: '检测路径',
                          })}
                        </Button>
                      </div>
                      <FormDescription>
                        {t('form.media_directory_desc', {
                          defaultValue:
                            '录像、抓拍与临时导出文件的落盘根路径。若修改路径，新文件立即写入新目录，旧目录历史视频仍可点播并优先淘汰。',
                        })}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {/* Path Test Result Feedback Banner */}
                {testResult && (
                  <div
                    className={cn(
                      'space-y-1.5 rounded-lg border p-3 text-xs',
                      statusLevelClass(
                        testResult.writable ? 'active' : 'destructive'
                      )
                    )}
                  >
                    <div className='flex items-center justify-between'>
                      <div className='flex items-center gap-1.5 font-semibold'>
                        {testResult.writable ? (
                          <>
                            <CheckCircle2 className='size-4' />
                            <span>
                              {t('form.path_valid', {
                                defaultValue: '目录合法且读写正常',
                              })}
                            </span>
                          </>
                        ) : (
                          <>
                            <AlertCircle className='size-4' />
                            <span>
                              {t('form.path_invalid', {
                                defaultValue: '路径检测未通过',
                              })}
                            </span>
                          </>
                        )}
                      </div>
                      <div className='flex items-center gap-2'>
                        {testResult.isExternal ? (
                          <Badge
                            variant='outline'
                            className='border-primary/20 bg-primary/10 text-primary'
                          >
                            {t('overview.dedicated_mount', {
                              defaultValue: '独立外挂盘',
                            })}
                          </Badge>
                        ) : (
                          <Badge
                            variant='outline'
                            className='border-amber-500/30 text-amber-500'
                          >
                            {t('overview.root_fs', {
                              defaultValue: '系统根分区',
                            })}
                          </Badge>
                        )}
                      </div>
                    </div>
                    {testResult.writable ? (
                      <div className='flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground dark:text-emerald-300/80'>
                        <span>
                          {t('overview.mountPoint', { defaultValue: '挂载点' })}
                          : <strong>{testResult.mountPoint}</strong> (
                          {testResult.fsType})
                        </span>
                        <span>
                          {t('overview.total', { defaultValue: '总容量' })}:{' '}
                          <strong>{formatBytes(testResult.totalBytes)}</strong>
                        </span>
                        <span>
                          {t('overview.free', { defaultValue: '可用' })}:{' '}
                          <strong>{formatBytes(testResult.freeBytes)}</strong>
                        </span>
                      </div>
                    ) : (
                      <p className='font-mono text-xs'>
                        {testResult.errorReason}
                      </p>
                    )}
                  </div>
                )}
              </div>
            ) : (
              <S3BackendFields
                control={form.control}
                hasSavedSecret={initialConfig.s3?.hasSecretAccessKey ?? false}
                isTestingConnection={connectionMutation.isPending}
                connectionResult={connectionResult}
                onApplyPreset={handleApplyPreset}
                onTestConnection={() => void handleTestConnection()}
                onClearConnectionResult={() => setConnectionResult(null)}
              />
            )}

            {/* Core Settings: Retention Policies */}
            <div className='flex flex-col gap-4 sm:flex-row sm:items-start'>
              <FormField
                control={form.control}
                name='recordingsRetentionDays'
                render={({ field }) => (
                  <FormItem className='min-w-0 flex-1'>
                    <FormLabel>
                      {t('form.recordings_days', {
                        defaultValue: '视频录像保留天数',
                      })}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={365}
                        {...field}
                        onChange={(e) => field.onChange(Number(e.target.value))}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('form.recordings_days_desc', {
                        defaultValue: '常规录像最大留存（默认 15 天）',
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='snapshotsRetentionDays'
                render={({ field }) => (
                  <FormItem className='min-w-0 flex-1'>
                    <FormLabel>
                      {t('form.snapshots_days', {
                        defaultValue: 'AI抓拍保留天数',
                      })}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={730}
                        {...field}
                        onChange={(e) => field.onChange(Number(e.target.value))}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('form.snapshots_days_desc', {
                        defaultValue:
                          'AI 抓拍最大留存（默认 90 天）；空间不足时会提前回收未锁定抓拍，告警/锁定图除外',
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='exportsRetentionHours'
                render={({ field }) => (
                  <FormItem className='min-w-0 flex-1'>
                    <FormLabel>
                      {t('form.exports_hours', {
                        defaultValue: '临时导出保留小时',
                      })}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={168}
                        {...field}
                        onChange={(e) => field.onChange(Number(e.target.value))}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('form.exports_hours_desc', {
                        defaultValue: '导出剪辑保留生存期（默认 48 小时）',
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            {/* Advanced Settings: Collapsible Accordion */}
            <Collapsible
              open={advancedOpen}
              onOpenChange={setAdvancedOpen}
              className='space-y-4 rounded-lg border border-border/60 bg-muted/20 p-4'
            >
              <div className='flex items-center justify-between'>
                <div className='flex items-center gap-2'>
                  <Sliders className='size-4 text-muted-foreground' />
                  <span className='text-sm font-semibold'>
                    {t('advanced.title', {
                      defaultValue: '高级双水位与紧急熔断策略',
                    })}
                  </span>
                </div>
                <CollapsibleTrigger asChild>
                  <Button variant='ghost' size='sm' className='h-8 w-8 p-0'>
                    <ChevronDown
                      className={`size-4 transition-transform duration-200 ${
                        advancedOpen ? 'rotate-180' : ''
                      }`}
                    />
                    <span className='sr-only'>Toggle Advanced Settings</span>
                  </Button>
                </CollapsibleTrigger>
              </div>

              <CollapsibleContent className='space-y-4 pt-2'>
                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='highWatermarkPercent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('advanced.high_watermark', {
                            defaultValue: '高水位清理激活阈值 (%)',
                          })}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={50}
                            max={95}
                            {...field}
                            onChange={(e) =>
                              field.onChange(Number(e.target.value))
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.high_watermark_desc', {
                            defaultValue:
                              '触碰该使用率时后台协程自动唤醒清理（默认 90%）',
                          })}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='lowWatermarkPercent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('advanced.low_watermark', {
                            defaultValue: '低水位清理休眠阈值 (%)',
                          })}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={40}
                            max={85}
                            {...field}
                            onChange={(e) =>
                              field.onChange(Number(e.target.value))
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.low_watermark_desc', {
                            defaultValue:
                              '容量回落到该百分比后停止清理进入休眠（默认 80%）',
                          })}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='emergencyStopPercent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('advanced.emergency_percent', {
                            defaultValue: '紧急停录使用率红线 (%)',
                          })}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={90}
                            max={99}
                            {...field}
                            onChange={(e) =>
                              field.onChange(Number(e.target.value))
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.emergency_percent_desc', {
                            defaultValue:
                              '达到该极高使用率时强制熔断停录，保全数据库（默认 95%）',
                          })}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='emergencyStopMinMb'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('advanced.emergency_mb', {
                            defaultValue: '紧急停录最小剩余空间 (MB)',
                          })}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={100}
                            max={102400}
                            {...field}
                            onChange={(e) =>
                              field.onChange(Number(e.target.value))
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.emergency_mb_desc', {
                            defaultValue:
                              '可用空间不足此容量时触发停录保护（默认 2048 MB）',
                          })}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
              </CollapsibleContent>
            </Collapsible>

            {/* Bottom Actions */}
            <div className='flex items-center justify-end gap-2 pt-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => {
                  form.reset(configToFormValues(initialConfig))
                  setTestResult(null)
                  setConnectionResult(null)
                }}
                disabled={updateMutation.isPending}
                className='gap-1.5'
              >
                <RotateCcw className='size-3.5 text-muted-foreground' />
                {t('form.btn_reset', { defaultValue: '重置更改' })}
              </Button>
              <Button
                type='submit'
                size='sm'
                disabled={updateMutation.isPending}
                className='gap-1.5'
              >
                {updateMutation.isPending ? (
                  <Loader2 className='size-3.5 animate-spin' />
                ) : (
                  <Save className='size-3.5' />
                )}
                {t('form.btn_save', { defaultValue: '保存配置' })}
              </Button>
            </div>
          </form>
        </Form>
      </CardContent>
    </Card>
  )
}
