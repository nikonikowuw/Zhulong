import { type Control, useWatch } from 'react-hook-form'
import {
  AlertCircle,
  CheckCircle2,
  Cloud,
  Loader2,
  PlugZap,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { type ConnectionTestResponse } from '../types'
import { type StorageFormValues } from './storage-form-schema'

interface S3BackendFieldsProps {
  control: Control<StorageFormValues>
  hasSavedSecret: boolean
  isTestingConnection: boolean
  connectionResult: ConnectionTestResponse | null
  onApplyPreset: (preset: 'minio' | 'cloud') => void
  onTestConnection: () => void
  onClearConnectionResult: () => void
}

export function S3BackendFields({
  control,
  hasSavedSecret,
  isTestingConnection,
  connectionResult,
  onApplyPreset,
  onTestConnection,
  onClearConnectionResult,
}: S3BackendFieldsProps) {
  const { t } = useTranslation('storage')
  const useTls = useWatch({ control, name: 's3.useTls' })

  return (
    <div className='space-y-4 rounded-lg border border-border/60 bg-muted/20 p-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <Cloud className='size-4 text-primary' />
          <span className='text-sm font-semibold'>
            {t('s3.section_title', {
              defaultValue: '对象存储连接参数与访问投递方式',
            })}
          </span>
        </div>
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => onApplyPreset('minio')}
          >
            {t('s3.preset_minio', { defaultValue: 'MinIO 常用预设' })}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => onApplyPreset('cloud')}
          >
            {t('s3.preset_cloud', { defaultValue: '公有云 OSS/S3 预设' })}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='secondary'
            onClick={onTestConnection}
            disabled={isTestingConnection}
            className='gap-1.5'
          >
            {isTestingConnection ? (
              <Loader2 className='size-3.5 animate-spin' />
            ) : (
              <PlugZap className='size-3.5' />
            )}
            {t('s3.btn_test_connection', { defaultValue: '测试连通性' })}
          </Button>
        </div>
      </div>

      {connectionResult && (
        <div
          className={cn(
            'space-y-1 rounded-lg border p-3 text-xs',
            statusLevelClass(
              connectionResult.writable ? 'active' : 'destructive'
            )
          )}
        >
          <div className='flex items-center gap-1.5 font-semibold'>
            {connectionResult.writable ? (
              <>
                <CheckCircle2 className='size-4' />
                <span>
                  {t('s3.conn_valid', {
                    defaultValue: '对象存储连通且具备读写权限',
                  })}
                </span>
              </>
            ) : (
              <>
                <AlertCircle className='size-4' />
                <span>
                  {t('s3.conn_invalid', {
                    defaultValue: '对象存储连通性检测失败',
                  })}
                </span>
              </>
            )}
          </div>
          {!connectionResult.writable && connectionResult.errorReason && (
            <p className='font-mono text-xs'>{connectionResult.errorReason}</p>
          )}
        </div>
      )}

      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
        <FormField
          control={control}
          name='s3.endpoint'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.endpoint', { defaultValue: '服务 Endpoint 地址' })}
              </FormLabel>
              <FormControl>
                <Input
                  placeholder={t('s3.endpoint_placeholder')}
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.region'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.region', { defaultValue: '区域 (Region)' })}
              </FormLabel>
              <FormControl>
                <Input
                  placeholder={t('s3.region_placeholder')}
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.bucket'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.bucket', { defaultValue: '存储桶名称 (Bucket)' })}
              </FormLabel>
              <FormControl>
                <Input
                  placeholder={t('s3.bucket_placeholder')}
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.prefix'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.prefix', { defaultValue: '对象键前缀（可选）' })}
              </FormLabel>
              <FormControl>
                <Input
                  placeholder={t('s3.prefix_placeholder')}
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.accessKeyId'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.access_key_id', { defaultValue: 'Access Key ID' })}
              </FormLabel>
              <FormControl>
                <Input
                  autoComplete='off'
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.secretAccessKey'
          render={({ field }) => (
            <FormItem>
              <div className='flex items-center justify-between gap-2'>
                <FormLabel>
                  {t('s3.secret_access_key', {
                    defaultValue: 'Secret Access Key',
                  })}
                </FormLabel>
                {hasSavedSecret && (
                  <Badge variant='outline' className='text-[11px]'>
                    {t('s3.secret_saved_placeholder', {
                      defaultValue: '已保存加密密钥 — 留空则保持原密钥不变',
                    })}
                  </Badge>
                )}
              </div>
              <FormControl>
                <Input
                  type='password'
                  autoComplete='new-password'
                  placeholder={
                    hasSavedSecret
                      ? t('s3.secret_saved_placeholder')
                      : t('s3.secret_new_placeholder')
                  }
                  className='font-mono text-sm'
                  {...field}
                  onChange={(e) => {
                    field.onChange(e)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
              {hasSavedSecret && (
                <FormDescription>{t('s3.secret_saved_hint')}</FormDescription>
              )}
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      {/* S3 Transport Switches */}
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-3'>
        <FormField
          control={control}
          name='s3.usePathStyle'
          render={({ field }) => (
            <FormItem className='flex items-center justify-between gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
              <div className='space-y-0.5'>
                <FormLabel className='text-xs font-medium'>
                  {t('s3.use_path_style', {
                    defaultValue: '使用 Path-Style 寻址',
                  })}
                </FormLabel>
                <FormDescription className='text-xs'>
                  {t('s3.use_path_style_desc')}
                </FormDescription>
              </div>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={(checked) => {
                    field.onChange(checked)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.useTls'
          render={({ field }) => (
            <FormItem className='flex items-center justify-between gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
              <div className='space-y-0.5'>
                <FormLabel className='text-xs font-medium'>
                  {t('s3.use_tls', { defaultValue: '启用 TLS (HTTPS)' })}
                </FormLabel>
                <FormDescription className='text-xs'>
                  {t('s3.use_tls_desc')}
                </FormDescription>
              </div>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={(checked) => {
                    field.onChange(checked)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.insecureSkipVerify'
          render={({ field }) => (
            <FormItem className='flex items-center justify-between gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
              <div className='space-y-0.5'>
                <FormLabel className='text-xs font-medium'>
                  {t('s3.insecure_skip_verify', {
                    defaultValue: '跳过 TLS 证书校验',
                  })}
                </FormLabel>
                <FormDescription className='text-xs'>
                  {t('s3.insecure_skip_verify_desc')}
                </FormDescription>
              </div>
              <FormControl>
                <Switch
                  checked={field.value}
                  disabled={!useTls}
                  onCheckedChange={(checked) => {
                    field.onChange(checked)
                    onClearConnectionResult()
                  }}
                />
              </FormControl>
            </FormItem>
          )}
        />
      </div>

      {/* Delivery Mode & Local Fallback / Spool */}
      <FormField
        control={control}
        name='deliveryMode'
        render={({ field }) => (
          <FormItem className='space-y-2'>
            <FormLabel>
              {t('s3.delivery_mode', { defaultValue: '媒体投递模式' })}
            </FormLabel>
            <FormControl>
              <RadioGroup
                value={field.value}
                onValueChange={field.onChange}
                className='grid grid-cols-1 gap-3 sm:grid-cols-2'
              >
                <label className='flex cursor-pointer items-start gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
                  <RadioGroupItem value='proxy' className='mt-0.5' />
                  <div className='space-y-0.5'>
                    <span className='block text-sm font-medium'>
                      {t('s3.delivery_proxy')}
                    </span>
                    <span className='block text-xs text-muted-foreground'>
                      {t('s3.delivery_proxy_desc')}
                    </span>
                  </div>
                </label>

                <label className='flex cursor-pointer items-start gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
                  <RadioGroupItem value='redirect' className='mt-0.5' />
                  <div className='space-y-0.5'>
                    <span className='block text-sm font-medium'>
                      {t('s3.delivery_redirect')}
                    </span>
                    <span className='block text-xs text-muted-foreground'>
                      {t('s3.delivery_redirect_desc')}
                    </span>
                  </div>
                </label>
              </RadioGroup>
            </FormControl>
          </FormItem>
        )}
      />

      <div className='grid grid-cols-1 gap-4 sm:grid-cols-3'>
        <FormField
          control={control}
          name='spoolDirectory'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.spool_directory', {
                  defaultValue: '本地 Spool 暂存目录',
                })}
              </FormLabel>
              <FormControl>
                <Input className='font-mono text-sm' {...field} />
              </FormControl>
              <FormDescription>{t('s3.spool_directory_desc')}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='spoolMaxMb'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.spool_max_mb', {
                  defaultValue: '本地 Spool 容量上限 (MB)',
                })}
              </FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={128}
                  max={1048576}
                  {...field}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={control}
          name='s3.quotaGb'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('s3.quota_gb', {
                  defaultValue: '远端存储桶配额上限 (GB)',
                })}
              </FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  {...field}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                />
              </FormControl>
              <FormDescription>{t('s3.quota_gb_desc')}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      <FormField
        control={control}
        name='localFallback'
        render={({ field }) => (
          <FormItem className='flex items-center justify-between gap-3 rounded-md border border-border/60 bg-background/60 p-3'>
            <div className='space-y-0.5'>
              <FormLabel className='text-sm font-medium'>
                {t('s3.local_fallback', { defaultValue: '本地历史兜底回退读' })}
              </FormLabel>
              <FormDescription className='text-xs'>
                {t('s3.local_fallback_desc')}
              </FormDescription>
            </div>
            <FormControl>
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            </FormControl>
          </FormItem>
        )}
      />
    </div>
  )
}
