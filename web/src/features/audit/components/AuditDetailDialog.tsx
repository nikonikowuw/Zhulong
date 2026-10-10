import { useState } from 'react'
import { AlertCircle, Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { StatusBadge } from './AuditColumnsCells'
import { AuditJsonViewer } from './AuditJsonViewer'
import { useAuditContext } from './AuditProvider'

export function AuditDetailDialog() {
  const { t, i18n } = useTranslation('audit')
  const { selectedLog, isDetailOpen, setIsDetailOpen } = useAuditContext()
  const [copied, setCopied] = useState(false)

  if (!selectedLog) {
    return null
  }

  let formattedDate = selectedLog.createdAt
  try {
    const d = new Date(selectedLog.createdAt)
    if (!isNaN(d.getTime())) {
      formattedDate = new Intl.DateTimeFormat(i18n.language || 'zh-Hans', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(d)
    }
  } catch {
    // fallback
  }

  let detailText = selectedLog.detail
  let detailIsJson = false
  if (selectedLog.detail) {
    try {
      detailText = JSON.stringify(JSON.parse(selectedLog.detail), null, 2)
      detailIsJson = true
    } catch {
      // Keep raw string fallback
    }
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(detailText)
      setCopied(true)
      toast.success(t('toast.copySuccess'))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t('toast.copyFailed'))
    }
  }

  return (
    <Sheet open={isDetailOpen} onOpenChange={setIsDetailOpen}>
      <SheetContent
        side='right'
        className='flex w-full flex-col gap-0 p-0 sm:max-w-xl'
      >
        <SheetHeader className='border-b border-border p-6 text-start'>
          <SheetTitle className='flex items-center gap-2'>
            <span>{t('dialog.detailTitle')}</span>
            <span className='font-mono text-sm font-normal text-muted-foreground'>
              #{selectedLog.id}
            </span>
          </SheetTitle>
          <SheetDescription>{t('dialog.detailDesc')}</SheetDescription>
        </SheetHeader>

        <div className='flex flex-1 flex-col gap-6 overflow-y-auto p-6'>
          {/* 核心元数据概览 */}
          <div className='grid grid-cols-2 gap-4 rounded-lg border border-border bg-muted/30 p-4 text-xs sm:grid-cols-3'>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.createdAt')}
              </span>
              <p className='mt-1 font-mono font-medium text-foreground'>
                {formattedDate}
              </p>
            </div>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.username')}
              </span>
              <p className='mt-1 font-medium text-foreground'>
                {selectedLog.username || '-'}
              </p>
            </div>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.status')}
              </span>
              <div className='mt-1'>
                <StatusBadge status={selectedLog.status} />
              </div>
            </div>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.action')}
              </span>
              <div className='mt-1 flex flex-col gap-0.5'>
                <p className='text-xs font-medium text-foreground'>
                  {t(`actionNames.${selectedLog.action}`, {
                    defaultValue: selectedLog.action,
                  })}
                </p>
                {t(`actionNames.${selectedLog.action}`, {
                  defaultValue: selectedLog.action,
                }) !== selectedLog.action && (
                  <span className='font-mono text-[11px] text-muted-foreground'>
                    {selectedLog.action}
                  </span>
                )}
              </div>
            </div>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.target')}
              </span>
              <p className='mt-1 font-mono font-medium text-foreground'>
                {selectedLog.target || '-'}
              </p>
            </div>
            <div>
              <span className='text-muted-foreground'>
                {t('table.columns.ip')}
              </span>
              <p className='mt-1 font-mono font-medium text-foreground'>
                {selectedLog.ip || '-'}
              </p>
            </div>
          </div>

          {/* 异常错误信息警示 */}
          {selectedLog.errorMsg && (
            <Alert variant='destructive'>
              <AlertCircle className='h-4 w-4' />
              <AlertTitle>{t('dialog.errorMsgTitle')}</AlertTitle>
              <AlertDescription className='mt-1 font-mono text-xs break-all'>
                {selectedLog.errorMsg}
              </AlertDescription>
            </Alert>
          )}

          {/* 变更详情或请求负载 */}
          <div className='flex flex-1 flex-col gap-2'>
            <div className='flex items-center justify-between'>
              <span className='text-sm font-semibold text-foreground'>
                {t('dialog.detailPayload')}
              </span>
              {detailText && (
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-7 gap-1.5 px-2 text-xs'
                  onClick={handleCopy}
                >
                  {copied ? (
                    <Check className='h-3.5 w-3.5 text-emerald-500' />
                  ) : (
                    <Copy className='h-3.5 w-3.5' />
                  )}
                  {copied ? t('actions.copied') : t('actions.copyJson')}
                </Button>
              )}
            </div>

            {detailText ? (
              <pre className='flex-1 overflow-x-auto rounded-lg border border-border bg-muted/40 p-4 font-mono text-xs leading-relaxed text-foreground'>
                <AuditJsonViewer
                  value={detailText}
                  highlighted={detailIsJson}
                />
              </pre>
            ) : (
              <div className='flex h-32 items-center justify-center rounded-lg border border-dashed border-border bg-muted/10 text-xs text-muted-foreground'>
                {t('dialog.noDetail')}
              </div>
            )}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
