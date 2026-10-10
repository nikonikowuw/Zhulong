import type { ReactNode } from 'react'

interface AuditJsonViewerProps {
  value: string
  highlighted: boolean
}

const jsonTokenPattern =
  /("(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b)/g

function tokenClass(token: string, isKey: boolean): string {
  if (isKey) return 'text-sky-600 dark:text-sky-400'
  if (token.startsWith('"')) return 'text-emerald-700 dark:text-emerald-400'
  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(token)) {
    return 'text-amber-700 dark:text-amber-300'
  }
  if (token === 'true' || token === 'false' || token === 'null') {
    return 'text-violet-700 dark:text-violet-300'
  }
  return 'text-muted-foreground'
}

export function AuditJsonViewer({ value, highlighted }: AuditJsonViewerProps) {
  if (!highlighted) return <code>{value}</code>

  const parts: ReactNode[] = []
  let previousIndex = 0

  for (const match of value.matchAll(jsonTokenPattern)) {
    const token = match[0]
    const tokenIndex = match.index ?? previousIndex
    if (tokenIndex > previousIndex) {
      parts.push(value.slice(previousIndex, tokenIndex))
    }

    const isKey =
      token.startsWith('"') &&
      /^\s*:/.test(value.slice(tokenIndex + token.length))
    parts.push(
      <span className={tokenClass(token, isKey)} key={tokenIndex}>
        {token}
      </span>
    )
    previousIndex = tokenIndex + token.length
  }

  if (previousIndex < value.length) {
    parts.push(value.slice(previousIndex))
  }

  return <code>{parts}</code>
}
