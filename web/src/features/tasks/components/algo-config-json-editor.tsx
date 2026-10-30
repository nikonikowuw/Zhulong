import { AlertCircle, Check, Copy, RotateCcw, Terminal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVEL_CLASS,
  STATUS_TEXT_CLASS,
} from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

interface AlgoConfigJsonEditorProps {
  value: string
  isValid: boolean
  disabled: boolean
  copied: boolean
  errorDetail?: string | null
  onChange: (next: string) => void
  onCopy: () => void
  onFormat: () => void
}

const VALID_BADGE_CLASS = STATUS_LEVEL_CLASS.active
const INVALID_BADGE_CLASS = STATUS_LEVEL_CLASS.destructive

/** JSON 源码模式：格式化、复制与实时语法状态。 */
export function AlgoConfigJsonEditor({
  value,
  isValid,
  disabled,
  copied,
  errorDetail,
  onChange,
  onCopy,
  onFormat,
}: AlgoConfigJsonEditorProps) {
  const { t } = useTranslation('tasks')

  return (
    <div className='overflow-hidden'>
      <div className='flex items-center justify-between border-b border-border bg-muted/40 px-3 py-1.5'>
        <div className='flex items-center gap-2'>
          <Terminal className='h-3.5 w-3.5 text-muted-foreground' />
          <span className='font-mono text-[11px] font-medium text-muted-foreground'>
            config.json
          </span>
          <Badge
            variant='outline'
            title={!isValid && errorDetail ? errorDetail : undefined}
            className={cn(
              'gap-1.5 px-1.5 py-0 text-[10px] font-normal transition-colors',
              isValid ? VALID_BADGE_CLASS : INVALID_BADGE_CLASS
            )}
          >
            <span
              className={cn(
                'inline-block h-1.5 w-1.5 rounded-full',
                isValid ? STATUS_DOT_CLASS.active : STATUS_DOT_CLASS.destructive
              )}
            />
            {isValid
              ? t('dynamicForm.syntaxValid', '格式正确')
              : t('dynamicForm.syntaxError', '语法错误')}
          </Badge>
        </div>

        <div className='flex items-center gap-1'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={disabled}
            onClick={onCopy}
            className='h-6 gap-1 px-2 text-[11px] text-muted-foreground'
          >
            {copied ? (
              <Check className={cn('h-3 w-3', STATUS_TEXT_CLASS.active)} />
            ) : (
              <Copy className='h-3 w-3' />
            )}
            <span>
              {copied
                ? t('dynamicForm.copied', '已复制')
                : t('dynamicForm.copy', '复制')}
            </span>
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={disabled}
            onClick={onFormat}
            className='h-6 gap-1 px-2 text-[11px] text-muted-foreground'
          >
            <RotateCcw className='h-3 w-3' />
            <span>{t('dynamicForm.formatJson', '格式化')}</span>
          </Button>
        </div>
      </div>

      <Textarea
        rows={6}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder='{}'
        className='max-h-[360px] min-h-[140px] resize-y rounded-none border-0 bg-background font-mono text-xs leading-relaxed shadow-none focus-visible:ring-0'
      />

      {!isValid && errorDetail && (
        <div className='flex items-center gap-1.5 border-t border-destructive/20 bg-destructive/10 px-3 py-1.5 font-mono text-[11px] text-destructive'>
          <AlertCircle className='size-3.5 shrink-0' />
          <span className='truncate'>{errorDetail}</span>
        </div>
      )}
    </div>
  )
}
