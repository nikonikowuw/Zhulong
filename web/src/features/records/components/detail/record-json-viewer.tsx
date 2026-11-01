import { useState, type ReactNode } from 'react'
import { Check, Code2, Copy, Table2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

export interface RecordJsonViewerProps {
  data: Record<string, unknown>
  className?: string
  defaultMode?: 'structured' | 'code'
}

const jsonTokenPattern =
  /("(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b)/g

function tokenClass(token: string, isKey: boolean): string {
  if (isKey) return 'text-sky-600 dark:text-sky-400 font-semibold'
  if (token.startsWith('"')) return 'text-emerald-700 dark:text-emerald-400'
  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(token)) {
    return 'text-amber-700 dark:text-amber-300'
  }
  if (token === 'true' || token === 'false' || token === 'null') {
    return 'text-violet-700 dark:text-violet-300'
  }
  return 'text-muted-foreground'
}

function renderValue(val: unknown): ReactNode {
  if (typeof val === 'boolean') {
    return (
      <Badge
        variant='outline'
        className={cn(
          'font-mono text-[11px]',
          val
            ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:border-emerald-500/40 dark:bg-emerald-500/20 dark:text-emerald-400'
            : 'border-neutral-300 bg-neutral-100 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800'
        )}
      >
        {String(val)}
      </Badge>
    )
  }
  if (typeof val === 'number') {
    return (
      <span className='font-mono font-semibold text-amber-600 dark:text-amber-400'>
        {val}
      </span>
    )
  }
  if (typeof val === 'string') {
    return <span className='font-mono break-all text-foreground'>{val}</span>
  }
  if (val === null || val === undefined) {
    return (
      <span className='font-mono text-xs text-muted-foreground italic'>
        null
      </span>
    )
  }
  return (
    <span className='font-mono text-xs break-all text-muted-foreground'>
      {JSON.stringify(val)}
    </span>
  )
}

export function RecordJsonViewer({
  data,
  className,
  defaultMode = 'structured',
}: RecordJsonViewerProps) {
  const { t } = useTranslation('records')
  const [mode, setMode] = useState<'structured' | 'code'>(defaultMode)
  const [copied, setCopied] = useState(false)

  const entries = Object.entries(data || {})
  const hasData = entries.length > 0

  if (!hasData) {
    return (
      <div className='rounded-md border border-dashed border-border p-4 text-center text-xs text-muted-foreground'>
        {t('detail.emptyAttributes')}
      </div>
    )
  }

  const jsonString = JSON.stringify(data, null, 2)

  const handleCopy = async () => {
    const success = await copyToClipboard(jsonString)
    if (success) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  const renderCode = () => {
    const parts: ReactNode[] = []
    let previousIndex = 0

    for (const match of jsonString.matchAll(jsonTokenPattern)) {
      const token = match[0]
      const tokenIndex = match.index ?? previousIndex
      if (tokenIndex > previousIndex) {
        parts.push(jsonString.slice(previousIndex, tokenIndex))
      }

      const isKey =
        token.startsWith('"') &&
        /^\s*:/.test(jsonString.slice(tokenIndex + token.length))
      parts.push(
        <span className={tokenClass(token, isKey)} key={tokenIndex}>
          {token}
        </span>
      )
      previousIndex = tokenIndex + token.length
    }

    if (previousIndex < jsonString.length) {
      parts.push(jsonString.slice(previousIndex))
    }

    return (
      <pre className='max-h-56 overflow-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-relaxed select-text'>
        <code>{parts}</code>
      </pre>
    )
  }

  return (
    <div className={cn('space-y-2', className)}>
      {/* 视图切换与复制快捷栏 */}
      <div className='flex items-center justify-between gap-2 text-xs'>
        <div className='flex items-center rounded-md border border-border bg-muted/40 p-0.5'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className={cn(
              'h-6 px-2 text-[11px] font-medium transition-all',
              mode === 'structured'
                ? 'bg-background text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'
            )}
            onClick={() => setMode('structured')}
          >
            <Table2 className='me-1 size-3' />
            {t('detail.viewStructured')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className={cn(
              'h-6 px-2 text-[11px] font-medium transition-all',
              mode === 'code'
                ? 'bg-background text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'
            )}
            onClick={() => setMode('code')}
          >
            <Code2 className='me-1 size-3' />
            {t('detail.viewJson')}
          </Button>
        </div>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground'
          onClick={() => void handleCopy()}
        >
          {copied ? (
            <Check className='me-1 size-3 text-emerald-500' />
          ) : (
            <Copy className='me-1 size-3' />
          )}
          {copied ? t('detail.jsonCopied') : t('detail.copyJson')}
        </Button>
      </div>

      {mode === 'structured' ? (
        <div className='max-h-56 divide-y divide-border/60 overflow-y-auto rounded-md border border-border bg-muted/20'>
          {entries.map(([key, val]) => (
            <div
              key={key}
              className='flex items-center justify-between gap-3 px-3 py-2 text-xs hover:bg-muted/30'
            >
              <span className='font-mono font-medium text-muted-foreground'>
                {key}
              </span>
              <div className='text-end font-medium text-foreground'>
                {renderValue(val)}
              </div>
            </div>
          ))}
        </div>
      ) : (
        renderCode()
      )}
    </div>
  )
}
