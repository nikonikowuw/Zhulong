import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface RecordJsonViewerProps {
  data: Record<string, unknown>
  className?: string
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

export function RecordJsonViewer({ data, className }: RecordJsonViewerProps) {
  if (!data || Object.keys(data).length === 0) {
    return (
      <div className='rounded border border-dashed border-border p-3 text-center text-xs text-muted-foreground'>
        暂无结构化参数
      </div>
    )
  }

  const jsonString = JSON.stringify(data, null, 2)
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
    <pre
      className={cn(
        'max-h-60 overflow-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-relaxed select-text',
        className
      )}
    >
      <code>{parts}</code>
    </pre>
  )
}
