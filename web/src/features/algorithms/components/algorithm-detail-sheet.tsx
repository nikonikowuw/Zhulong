import { useMemo, useState } from 'react'
import {
  Activity,
  Check,
  Code2,
  Copy,
  Cpu,
  FileJson,
  FlaskConical,
  HardDrive,
  Info,
  ShieldCheck,
  Tag,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { parseConfigSchema, parseManifest } from '../data/detail-parser'
import { type Algorithm } from '../data/schema'
import { AlgorithmStatusBadge } from './algorithm-status-badge'
import { AlgorithmTaskTypeCell } from './algorithms-columns-cells'

interface AlgorithmDetailSheetProps {
  algorithm: Algorithm | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onOpenTrial?: (algorithm: Algorithm) => void
}

export function AlgorithmDetailSheet({
  algorithm,
  open,
  onOpenChange,
  onOpenTrial,
}: AlgorithmDetailSheetProps): React.JSX.Element | null {
  const { t } = useTranslation('algorithms')
  const [copiedId, setCopiedId] = useState(false)
  const [copiedManifest, setCopiedManifest] = useState(false)
  const [copiedSchema, setCopiedSchema] = useState(false)

  const parsedManifest = useMemo(
    () => parseManifest(algorithm?.manifestJson),
    [algorithm?.manifestJson]
  )

  const parsedConfig = useMemo(
    () => parseConfigSchema(algorithm?.configSchemaJson),
    [algorithm?.configSchemaJson]
  )

  if (!algorithm) return null

  const handleCopy = async (
    text: string,
    setCopied: (v: boolean) => void,
    successKey: string,
    failKey: string
  ) => {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopied(true)
      toast.success(t(successKey))
      setTimeout(() => setCopied(false), 2000)
    } else {
      toast.error(t(failKey))
    }
  }

  const handleCopyId = () => {
    void handleCopy(
      algorithm.id,
      setCopiedId,
      'copyAlgorithmIdSuccess',
      'copyAlgorithmIdFailed'
    )
  }

  const handleCopyManifest = () => {
    void handleCopy(
      parsedManifest.rawFormatted,
      setCopiedManifest,
      'copyJsonSuccess',
      'copyJsonFailed'
    )
  }

  const handleCopySchema = () => {
    void handleCopy(
      parsedConfig.rawFormatted,
      setCopiedSchema,
      'copyJsonSuccess',
      'copyJsonFailed'
    )
  }

  const isTrialAllowed = algorithm.status === 'DISABLED'

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'
      >
        {/* 顶部标题区 */}
        <SheetHeader className='border-b p-5 pb-4'>
          <div className='flex items-start justify-between gap-3 pe-8'>
            <div className='min-w-0 flex-1 space-y-1'>
              <div className='flex flex-wrap items-center gap-2'>
                <SheetTitle className='text-lg font-bold text-foreground'>
                  {algorithm.name}
                </SheetTitle>
                <Badge variant='secondary' className='font-mono text-xs'>
                  v{algorithm.version}
                </Badge>
                <AlgorithmStatusBadge status={algorithm.status} />
              </div>

              <div className='flex items-center gap-1.5 pt-0.5 text-xs text-muted-foreground'>
                <span className='font-mono'>{algorithm.id}</span>
                <Button
                  variant='ghost'
                  size='icon'
                  className='size-6 text-muted-foreground hover:text-foreground'
                  onClick={handleCopyId}
                  title={t('copyAlgorithmId', '复制算法标识')}
                >
                  {copiedId ? (
                    <Check className='size-3 text-emerald-500' />
                  ) : (
                    <Copy className='size-3' />
                  )}
                  <span className='sr-only'>
                    {t('copyAlgorithmId', '复制算法标识')}
                  </span>
                </Button>
              </div>

              {algorithm.description && (
                <SheetDescription className='line-clamp-2 text-xs text-muted-foreground'>
                  {algorithm.description}
                </SheetDescription>
              )}
            </div>
          </div>
        </SheetHeader>

        {/* 主内容 Tab 区域 */}
        <Tabs
          defaultValue='manifest'
          className='flex flex-1 flex-col overflow-hidden'
        >
          <div className='border-b bg-muted/20 px-5 py-2.5'>
            <TabsList className='grid w-full grid-cols-3'>
              <TabsTrigger value='manifest' className='text-xs'>
                <FileJson className='me-1.5 size-3.5' />
                {t('tabManifest', '规格清单')}
              </TabsTrigger>
              <TabsTrigger value='schema' className='text-xs'>
                <Code2 className='me-1.5 size-3.5' />
                {t('tabConfigSchema', '参数规范')}
              </TabsTrigger>
              <TabsTrigger value='runtime' className='text-xs'>
                <Info className='me-1.5 size-3.5' />
                {t('tabRuntime', '运行与版本')}
              </TabsTrigger>
            </TabsList>
          </div>

          <div className='flex-1 overflow-y-auto p-5'>
            {/* Tab 1: 规格清单 */}
            <TabsContent value='manifest' className='m-0 space-y-5'>
              {/* 核心指标规格卡片 */}
              <div className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
                <div className='rounded-lg border bg-card p-3'>
                  <span className='text-xs text-muted-foreground'>
                    {t('thType', '任务形态')}
                  </span>
                  <div className='mt-1'>
                    <AlgorithmTaskTypeCell taskType={algorithm.taskType} />
                  </div>
                </div>

                <div className='rounded-lg border bg-card p-3'>
                  <span className='text-xs text-muted-foreground'>
                    {t('thPlatform', '目标平台')}
                  </span>
                  <div className='mt-1 flex items-center gap-1.5 font-mono text-xs font-semibold'>
                    <Cpu className='size-3.5 text-muted-foreground' />
                    <span>{algorithm.targetArch}</span>
                    <span className='text-muted-foreground'>/</span>
                    <span className='text-primary uppercase'>
                      {algorithm.targetDevice}
                    </span>
                  </div>
                </div>

                <div className='rounded-lg border bg-card p-3'>
                  <span className='text-xs text-muted-foreground'>
                    {t('motionGateSupport', '动检门控')}
                  </span>
                  <div className='mt-1'>
                    <Badge
                      variant='outline'
                      className={cn(
                        'text-xs',
                        algorithm.supportsMotionGate
                          ? 'border-emerald-500/30 text-emerald-600 dark:text-emerald-400'
                          : 'border-muted text-muted-foreground'
                      )}
                    >
                      <ShieldCheck className='me-1 size-3' />
                      {algorithm.supportsMotionGate
                        ? t('motionGateEnabled', '支持')
                        : t('motionGateDisabled', '不支持')}
                    </Badge>
                  </div>
                </div>

                <div className='rounded-lg border bg-card p-3'>
                  <span className='text-xs text-muted-foreground'>
                    {t('computeCostLabel', '算力消耗')}
                  </span>
                  <div className='mt-1 flex items-center gap-1 text-xs'>
                    <Activity className='size-3.5 text-muted-foreground' />
                    <span className='font-mono font-semibold'>
                      {algorithm.computeCost}
                    </span>
                    <span className='text-muted-foreground'>
                      {t('points', '点')}
                    </span>
                  </div>
                </div>

                <div className='rounded-lg border bg-card p-3'>
                  <span className='text-xs text-muted-foreground'>
                    {t('memoryLabel', '内存配额')}
                  </span>
                  <div className='mt-1 flex items-center gap-1 text-xs'>
                    <HardDrive className='size-3.5 text-muted-foreground' />
                    <span className='font-mono font-semibold'>
                      {algorithm.memoryMb}
                    </span>
                    <span className='text-muted-foreground'>MB</span>
                  </div>
                </div>
              </div>

              {/* 识别目标类别 Tags */}
              <div className='space-y-2 rounded-lg border bg-card p-4'>
                <div className='flex items-center gap-1.5 text-xs font-semibold text-foreground'>
                  <Tag className='size-3.5 text-muted-foreground' />
                  <span>{t('supportedLabels', '识别类别')}</span>
                </div>
                {parsedManifest.labels.length > 0 ? (
                  <div className='flex flex-wrap gap-1.5 pt-1'>
                    {parsedManifest.labels.map((label) => (
                      <Badge
                        key={label}
                        variant='secondary'
                        className='font-mono text-xs font-normal'
                      >
                        {label}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  <p className='text-xs text-muted-foreground'>
                    {t('noSpecificLabels', '未在清单中单独声明类别列表')}
                  </p>
                )}
              </div>

              {/* 原始 manifest.json 源码区 */}
              <div className='space-y-2'>
                <div className='flex items-center justify-between'>
                  <span className='text-xs font-semibold text-foreground'>
                    {t('rawManifestTitle', '原始 manifest.json')}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    className='h-7 gap-1.5 text-xs'
                    onClick={handleCopyManifest}
                  >
                    {copiedManifest ? (
                      <Check className='size-3 text-emerald-500' />
                    ) : (
                      <Copy className='size-3' />
                    )}
                    {t('copyJson', '复制 JSON')}
                  </Button>
                </div>
                <pre
                  data-testid='manifest-code-block'
                  className='max-h-64 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs text-foreground select-all'
                >
                  <code>{parsedManifest.rawFormatted}</code>
                </pre>
              </div>
            </TabsContent>

            {/* Tab 2: 参数规范 */}
            <TabsContent value='schema' className='m-0 space-y-5'>
              {parsedConfig.isZeroConfig ? (
                <div className='rounded-lg border border-dashed p-6 text-center'>
                  <Code2 className='mx-auto size-8 text-muted-foreground/60' />
                  <h4 className='mt-2 text-sm font-semibold text-foreground'>
                    {t('zeroConfigTitle', '零参算法')}
                  </h4>
                  <p className='mt-1 text-xs text-muted-foreground'>
                    {t(
                      'zeroConfigDesc',
                      '该算法未声明任何可配置的高级超参数，在分析任务中直接按需启用即可。'
                    )}
                  </p>
                </div>
              ) : (
                <div className='space-y-3'>
                  <div className='overflow-hidden rounded-lg border'>
                    <table className='w-full text-start text-xs'>
                      <thead className='border-b bg-muted/50 font-medium text-muted-foreground'>
                        <tr>
                          <th className='p-2.5 ps-3 text-start'>
                            {t('propName', '显示名称')} / {t('propKey', '键名')}
                          </th>
                          <th className='p-2.5 text-start'>
                            {t('propType', '类型')}
                          </th>
                          <th className='p-2.5 text-start'>
                            {t('propRange', '约束范围 / 枚举')}
                          </th>
                          <th className='p-2.5 text-start'>
                            {t('propDefault', '默认值')}
                          </th>
                        </tr>
                      </thead>
                      <tbody className='divide-y divide-border'>
                        {parsedConfig.properties.map((prop) => (
                          <tr key={prop.key} className='hover:bg-muted/20'>
                            <td className='p-2.5 ps-3'>
                              <div className='flex items-center gap-1.5'>
                                <span className='font-medium text-foreground'>
                                  {prop.title || prop.key}
                                </span>
                                {prop.required && (
                                  <Badge
                                    variant='destructive'
                                    className='px-1 py-0 text-[10px]'
                                  >
                                    {t('requiredTag', '必填')}
                                  </Badge>
                                )}
                              </div>
                              <span className='font-mono text-[11px] text-muted-foreground'>
                                {prop.key}
                              </span>
                              {prop.description && (
                                <p className='mt-0.5 text-[11px] text-muted-foreground/80'>
                                  {prop.description}
                                </p>
                              )}
                            </td>
                            <td className='p-2.5 font-mono text-muted-foreground'>
                              {prop.type}
                            </td>
                            <td className='p-2.5'>
                              {prop.range ? (
                                <span className='font-mono text-muted-foreground'>
                                  {prop.range}
                                </span>
                              ) : prop.enumValues ? (
                                <div className='flex flex-wrap gap-1'>
                                  {prop.enumValues.map((val) => (
                                    <Badge
                                      key={val}
                                      variant='outline'
                                      className='font-mono text-[10px]'
                                    >
                                      {val}
                                    </Badge>
                                  ))}
                                </div>
                              ) : (
                                <span className='text-muted-foreground'>-</span>
                              )}
                            </td>
                            <td className='p-2.5 font-mono text-muted-foreground'>
                              {prop.defaultValue !== undefined ? (
                                <span className='rounded bg-muted px-1.5 py-0.5'>
                                  {prop.defaultValue}
                                </span>
                              ) : (
                                '-'
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}

              {/* 原始 config.schema.json */}
              <div className='space-y-2'>
                <div className='flex items-center justify-between'>
                  <span className='text-xs font-semibold text-foreground'>
                    {t('rawSchemaTitle', '原始 config.schema.json')}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    className='h-7 gap-1.5 text-xs'
                    onClick={handleCopySchema}
                  >
                    {copiedSchema ? (
                      <Check className='size-3 text-emerald-500' />
                    ) : (
                      <Copy className='size-3' />
                    )}
                    {t('copyJson', '复制 JSON')}
                  </Button>
                </div>
                <pre
                  data-testid='schema-code-block'
                  className='max-h-64 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs text-foreground select-all'
                >
                  <code>{parsedConfig.rawFormatted}</code>
                </pre>
              </div>
            </TabsContent>

            {/* Tab 3: 运行与版本 */}
            <TabsContent value='runtime' className='m-0 space-y-4'>
              <div className='space-y-3 rounded-lg border bg-card p-4'>
                <div className='flex items-center justify-between border-b pb-2 text-xs'>
                  <span className='text-muted-foreground'>
                    {t('previousVersionLabel', '上一稳定版本')}
                  </span>
                  <span className='font-mono font-medium text-foreground'>
                    {algorithm.previousVersion
                      ? `v${algorithm.previousVersion}`
                      : t('nonePreviousVersion', '无（首发版本）')}
                  </span>
                </div>

                <div className='flex items-center justify-between border-b pb-2 text-xs'>
                  <span className='text-muted-foreground'>
                    {t('installedAt', '安装时间')}
                  </span>
                  <span className='font-mono text-foreground'>
                    {new Date(algorithm.createdAt).toLocaleString()}
                  </span>
                </div>

                <div className='flex items-center justify-between text-xs'>
                  <span className='text-muted-foreground'>
                    {t('updatedAt', '更新时间')}
                  </span>
                  <span className='font-mono text-foreground'>
                    {new Date(algorithm.updatedAt).toLocaleString()}
                  </span>
                </div>
              </div>

              {/* 试跑快捷行动点 */}
              {isTrialAllowed && onOpenTrial && (
                <div className='flex items-center justify-between rounded-lg border border-primary/20 bg-primary/5 p-3.5'>
                  <div className='space-y-0.5'>
                    <h5 className='text-xs font-semibold text-foreground'>
                      {t('trialAlgorithm', '效果试跑')}
                    </h5>
                    <p className='text-xs text-muted-foreground'>
                      {t(
                        'trialDialogDesc',
                        '上传单张静态图片，在隔离子进程中单次推理并预览目标。'
                      )}
                    </p>
                  </div>
                  <Button
                    size='sm'
                    className='gap-1.5 text-xs'
                    onClick={() => {
                      onOpenChange(false)
                      onOpenTrial(algorithm)
                    }}
                  >
                    <FlaskConical className='size-3.5' />
                    {t('openTrialFromDetail', '前往效果试跑')}
                  </Button>
                </div>
              )}
            </TabsContent>
          </div>
        </Tabs>
      </SheetContent>
    </Sheet>
  )
}
