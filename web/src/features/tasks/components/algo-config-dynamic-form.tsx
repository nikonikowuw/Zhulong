import { Code2, RotateCcw, SlidersHorizontal, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useAlgoConfigForm } from '../hooks/use-algo-config-form'
import { AlgoConfigField } from './algo-config-field'
import { AlgoConfigJsonEditor } from './algo-config-json-editor'

export interface AlgoConfigDynamicFormProps {
  schemaJson?: string
  value: string
  onChange: (nextJson: string) => void
  disabled?: boolean
  className?: string
}

/**
 * 算法配置双模态编辑器：表单模式（按 config.schema.json 动态渲染）
 * 与 JSON 源码模式双向切换，零参算法展示无配置提示。
 */
export function AlgoConfigDynamicForm({
  schemaJson,
  value,
  onChange,
  disabled = false,
  className,
}: AlgoConfigDynamicFormProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const config = useAlgoConfigForm({ schemaJson, value, onChange })
  const { form, propertyEntries, zeroConfig, mode, switchMode } = config

  return (
    <Form {...form}>
      <TooltipProvider delayDuration={200}>
        <div className={cn('space-y-2', className)}>
          {/* 顶部工具栏：模式切换与恢复默认值 */}
          <div className='flex items-center justify-between gap-2 px-0.5'>
            <div className='flex min-w-0 items-center gap-1.5'>
              <SlidersHorizontal className='h-3.5 w-3.5 shrink-0 text-muted-foreground' />
              <span className='truncate text-xs font-medium text-foreground'>
                {t('dialog.configJsonLabel', '算法运行参数')}
              </span>
              {!zeroConfig && (
                <Badge
                  variant='outline'
                  className='px-1.5 py-0 font-mono text-[10px] font-normal text-muted-foreground'
                >
                  {t('dynamicForm.fieldCount', {
                    count: propertyEntries.length,
                  })}
                </Badge>
              )}
            </div>

            <div className='flex shrink-0 items-center gap-2'>
              <div className='inline-flex items-center gap-0.5 rounded-md border border-border bg-muted/40 p-0.5'>
                <Button
                  type='button'
                  variant={mode === 'form' ? 'secondary' : 'ghost'}
                  size='sm'
                  disabled={disabled}
                  onClick={() => switchMode('form')}
                  className='h-6 gap-1 px-2 text-[11px]'
                >
                  <SlidersHorizontal className='h-3 w-3' />
                  <span>{t('dynamicForm.formMode', '表单模式')}</span>
                </Button>
                <Button
                  type='button'
                  variant={mode === 'json' ? 'secondary' : 'ghost'}
                  size='sm'
                  disabled={disabled}
                  onClick={() => switchMode('json')}
                  className='h-6 gap-1 px-2 text-[11px]'
                >
                  <Code2 className='h-3 w-3' />
                  <span>{t('dynamicForm.jsonMode', 'JSON 源码')}</span>
                </Button>
              </div>

              {!zeroConfig && (
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  disabled={disabled}
                  onClick={config.resetDefaults}
                  className='h-6 gap-1 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
                >
                  <RotateCcw className='h-3 w-3' />
                  <span>{t('dynamicForm.resetDefaults', '恢复默认值')}</span>
                </Button>
              )}
            </div>
          </div>

          <div className='overflow-hidden rounded-lg border border-border bg-muted/30'>
            {mode === 'json' ? (
              <AlgoConfigJsonEditor
                value={value}
                isValid={config.isValidJson}
                disabled={disabled}
                copied={config.copied}
                onChange={onChange}
                onCopy={config.copyJson}
                onFormat={config.formatJson}
              />
            ) : zeroConfig ? (
              <div className='flex flex-col items-center justify-center gap-1 px-3 py-4 text-center'>
                <div className='flex items-center gap-2 text-xs font-medium text-foreground'>
                  <Sparkles className='h-3.5 w-3.5 shrink-0 text-muted-foreground' />
                  <span>
                    {t(
                      'dynamicForm.zeroConfig',
                      '该算法无需额外参数配置（零参算法）'
                    )}
                  </span>
                </div>
                <p className='text-[11px] text-muted-foreground'>
                  {t('dynamicForm.zeroConfigDesc', '')}
                </p>
              </div>
            ) : (
              <div className='divide-y divide-border'>
                {propertyEntries.map(([propertyKey, prop]) => (
                  <AlgoConfigField
                    key={propertyKey}
                    control={form.control}
                    propertyKey={propertyKey}
                    prop={prop}
                    disabled={disabled}
                    onValueChange={config.setFieldValue}
                  />
                ))}
              </div>
            )}
          </div>
        </div>
      </TooltipProvider>
    </Form>
  )
}
