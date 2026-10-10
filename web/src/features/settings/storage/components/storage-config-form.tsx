import { useState } from 'react'
import { z } from 'zod'
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
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
  useTestStoragePath,
  useUpdateStorageConfig,
} from '../hooks/use-storage'
import { formatBytes } from '../utils/format'
import {
  type PathTestResponse,
  type StorageConfig,
  type StorageStatus,
} from '../types'

const storageFormSchema = z
  .object({
    mediaDirectory: z.string().trim().min(1, '请输入有效的媒体存储路径'),
    recordingsRetentionDays: z
      .number()
      .int()
      .min(1, '保留天数至少为 1 天')
      .max(365, '保留天数最多为 365 天'),
    snapshotsRetentionDays: z
      .number()
      .int()
      .min(1, '保留天数至少为 1 天')
      .max(730, '保留天数最多为 730 天'),
    exportsRetentionHours: z
      .number()
      .int()
      .min(1, '保留小时至少为 1 小时')
      .max(168, '保留小时最多为 168 小时'),
    highWatermarkPercent: z
      .number()
      .int()
      .min(50, '高水位至少为 50%')
      .max(95, '高水位最多为 95%'),
    lowWatermarkPercent: z
      .number()
      .int()
      .min(40, '低水位至少为 40%')
      .max(85, '低水位最多为 85%'),
    emergencyStopPercent: z
      .number()
      .int()
      .min(90, '紧急熔断阈值至少为 90%')
      .max(99, '紧急熔断阈值最多为 99%'),
    emergencyStopMinMb: z
      .number()
      .int()
      .min(100, '紧急保留空间至少为 100 MB')
      .max(102400, '紧急保留空间最多为 102400 MB'),
  })
  .refine((data) => data.lowWatermarkPercent < data.highWatermarkPercent, {
    message: '低水位休眠线必须严格小于高水位清理线',
    path: ['lowWatermarkPercent'],
  })
  .refine((data) => data.emergencyStopPercent >= data.highWatermarkPercent, {
    message: '紧急熔断停录阈值不能低于高水位清理线',
    path: ['emergencyStopPercent'],
  })

type StorageFormValues = z.infer<typeof storageFormSchema>

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

  const updateMutation = useUpdateStorageConfig()
  const testMutation = useTestStoragePath()

  const form = useForm<StorageFormValues>({
    resolver: zodResolver(storageFormSchema),
    defaultValues: {
      mediaDirectory: initialConfig.mediaDirectory,
      recordingsRetentionDays: initialConfig.recordingsRetentionDays,
      snapshotsRetentionDays: initialConfig.snapshotsRetentionDays,
      exportsRetentionHours: initialConfig.exportsRetentionHours,
      highWatermarkPercent: initialConfig.highWatermarkPercent,
      lowWatermarkPercent: initialConfig.lowWatermarkPercent,
      emergencyStopPercent: initialConfig.emergencyStopPercent,
      emergencyStopMinMb: initialConfig.emergencyStopMinMb,
    },
  })

  const currentPath = useWatch({
    control: form.control,
    name: 'mediaDirectory',
  })

  const handleTestPath = async () => {
    if (!currentPath) {
      toast.error(t('form.empty_path', { defaultValue: '请先输入目录路径' }))
      return
    }
    try {
      const res = await testMutation.mutateAsync({ path: currentPath })
      setTestResult(res)
      if (res.writable) {
        toast.success(
          t('form.test_success', {
            defaultValue: '路径检测通过，具备读写权能！',
          })
        )
      } else {
        toast.error(
          res.errorReason ||
            t('form.test_failed', {
              defaultValue: '该目录不存在或无写权限',
            })
        )
      }
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Path inspection failed'
      )
    }
  }

  const onSubmit = async (values: StorageFormValues) => {
    try {
      await updateMutation.mutateAsync(values)
      toast.success(
        t('form.save_success', { defaultValue: '存储策略配置已保存并生效！' })
      )
      if (onConfigUpdated) {
        onConfigUpdated()
      }
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update storage configuration'
      )
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
            {/* Core Settings: Media Directory */}
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
                          placeholder='/mnt/storage/media 或 data/media'
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
                        {t('form.btn_test_path', { defaultValue: '检测路径' })}
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
                  className={`rounded-lg border p-3 text-xs space-y-1.5 ${
                    testResult.writable
                      ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                      : 'border-destructive/20 bg-destructive/10 text-destructive'
                  }`}
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
                          className='bg-primary/10 text-primary border-primary/20'
                        >
                          {t('overview.dedicated_mount', {
                            defaultValue: '独立外挂盘',
                          })}
                        </Badge>
                      ) : (
                        <Badge
                          variant='outline'
                          className='text-amber-500 border-amber-500/30'
                        >
                          {t('overview.root_fs', { defaultValue: '系统根分区' })}
                        </Badge>
                      )}
                    </div>
                  </div>
                  {testResult.writable ? (
                    <div className='flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground dark:text-emerald-300/80'>
                      <span>
                        {t('overview.mountPoint', { defaultValue: '挂载点' })}:{' '}
                        <strong>{testResult.mountPoint}</strong> (
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
                    <p className='text-xs font-mono'>{testResult.errorReason}</p>
                  )}
                </div>
              )}
            </div>

            {/* Core Settings: Retention Policies */}
            <div className='grid grid-cols-1 gap-4 sm:grid-cols-3'>
              <FormField
                control={form.control}
                name='recordingsRetentionDays'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('form.recordings_days', { defaultValue: '视频录像保留天数' })}
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
                  <FormItem>
                    <FormLabel>
                      {t('form.snapshots_days', { defaultValue: 'AI抓拍保留天数' })}
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
                        defaultValue: '告警事件图最大留存（默认 90 天）',
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
                  <FormItem>
                    <FormLabel>
                      {t('form.exports_hours', { defaultValue: '临时导出保留小时' })}
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
              className='rounded-lg border border-border/60 bg-muted/20 p-4 space-y-4'
            >
              <div className='flex items-center justify-between'>
                <div className='flex items-center gap-2'>
                  <Sliders className='size-4 text-muted-foreground' />
                  <span className='text-sm font-semibold'>
                    {t('advanced.title', { defaultValue: '高级双水位与紧急熔断策略' })}
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
                            onChange={(e) => field.onChange(Number(e.target.value))}
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.high_watermark_desc', {
                            defaultValue: '触碰该使用率时后台协程自动唤醒清理（默认 90%）',
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
                            onChange={(e) => field.onChange(Number(e.target.value))}
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.low_watermark_desc', {
                            defaultValue: '容量回落到该百分比后停止清理进入休眠（默认 80%）',
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
                            onChange={(e) => field.onChange(Number(e.target.value))}
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.emergency_percent_desc', {
                            defaultValue: '达到该极高使用率时强制熔断停录，保全数据库（默认 95%）',
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
                            onChange={(e) => field.onChange(Number(e.target.value))}
                          />
                        </FormControl>
                        <FormDescription>
                          {t('advanced.emergency_mb_desc', {
                            defaultValue: '可用空间不足此容量时触发停录保护（默认 2048 MB）',
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
                  form.reset()
                  setTestResult(null)
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
