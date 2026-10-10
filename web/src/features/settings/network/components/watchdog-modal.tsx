import { useEffect, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  ExternalLink,
  Loader2,
  RotateCcw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useConfirmMutation, useRollbackMutation } from '../hooks/use-network'

export interface WatchdogTransactionData {
  transactionId: string
  timeoutSec: number
  targetUrl: string
  confirmToken: string
  expiresAt?: string
}

interface WatchdogModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  transaction: WatchdogTransactionData | null
  onConfirmed: () => void
  onRolledBack: () => void
}

function calculateInitialSeconds(tx: WatchdogTransactionData): number {
  if (tx.expiresAt) {
    const expMs = new Date(tx.expiresAt).getTime()
    const nowMs = Date.now()
    return Math.max(0, Math.floor((expMs - nowMs) / 1000))
  }
  return tx.timeoutSec || 60
}

interface WatchdogModalBodyProps {
  transaction: WatchdogTransactionData
  onOpenChange: (open: boolean) => void
  onConfirmed: () => void
  onRolledBack: () => void
}

function WatchdogModalBody({
  transaction,
  onOpenChange,
  onConfirmed,
  onRolledBack,
}: WatchdogModalBodyProps) {
  const { t } = useTranslation('network')
  const confirmMutation = useConfirmMutation()
  const rollbackMutation = useRollbackMutation()

  const totalSec = transaction.timeoutSec || 60
  const [timeLeft, setTimeLeft] = useState<number>(() =>
    calculateInitialSeconds(transaction)
  )

  const isExpired = timeLeft <= 0

  useEffect(() => {
    const timer = setInterval(() => {
      setTimeLeft((prev) => {
        if (prev <= 1) {
          clearInterval(timer)
          return 0
        }
        return prev - 1
      })
    }, 1000)

    return () => clearInterval(timer)
  }, [])

  // 判断是否需要跨 IP 迁移
  let isMigrating = false
  let migrationUrl = transaction.targetUrl || ''
  try {
    if (transaction.targetUrl) {
      const parsed = new URL(transaction.targetUrl)
      if (
        parsed.host &&
        window.location.host &&
        parsed.host !== window.location.host
      ) {
        isMigrating = true
        if (
          transaction.confirmToken &&
          !parsed.searchParams.has('confirm_token')
        ) {
          parsed.searchParams.set('confirm_token', transaction.confirmToken)
          migrationUrl = parsed.toString()
        }
      }
    }
  } catch {
    // 忽略非法 URL 格式
  }

  const handleConfirm = async () => {
    try {
      await confirmMutation.mutateAsync(transaction.confirmToken)
      toast.success(t('watchdog.confirmSuccess'))
      onOpenChange(false)
      onConfirmed()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('errors.confirmFailed')
      toast.error(msg)
    }
  }

  const handleRollback = async () => {
    try {
      await rollbackMutation.mutateAsync(transaction.confirmToken)
      toast.info(t('watchdog.rollbackSuccess'))
      onOpenChange(false)
      onRolledBack()
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : t('errors.rollbackFailed')
      toast.error(msg)
    }
  }

  const progressPercent = Math.max(
    0,
    Math.min(100, Math.round((timeLeft / totalSec) * 100))
  )

  return (
    <>
      <DialogHeader>
        <DialogTitle className='flex items-center gap-2'>
          <Clock className='h-5 w-5 text-amber-500' />
          <span>{t('watchdog.modalTitle')}</span>
        </DialogTitle>
        <DialogDescription>{t('watchdog.modalDesc')}</DialogDescription>
      </DialogHeader>

      <div className='space-y-4 py-2'>
        {/* 倒计时看板 */}
        <div className='space-y-2 rounded-md border bg-muted/40 p-4 text-center'>
          <div className='flex items-center justify-between text-xs text-muted-foreground'>
            <span>{t('watchdog.remainingTime')}</span>
            <span className='font-mono text-sm font-bold text-foreground'>
              {t('watchdog.seconds', { seconds: timeLeft })}
            </span>
          </div>

          <div className='h-2.5 w-full overflow-hidden rounded-full bg-secondary'>
            <div
              className={`h-full transition-all duration-1000 ${
                timeLeft > 20 ? 'bg-amber-500' : 'animate-pulse bg-destructive'
              }`}
              style={{ width: `${progressPercent}%` }}
            />
          </div>

          <div className='text-xs text-muted-foreground'>
            {t('watchdog.notice')}
          </div>
        </div>

        {/* 迁移提示 */}
        {isMigrating && !isExpired && (
          <Alert className='border-sky-300 bg-sky-200/40 text-sky-900 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-100'>
            <AlertTriangle className='h-4 w-4 text-sky-700 dark:text-sky-300' />
            <AlertTitle className='font-semibold'>
              {t('watchdog.migrationTitle')}
            </AlertTitle>
            <AlertDescription className='space-y-2 text-xs'>
              <p>{t('watchdog.migrationAlert')}</p>
              <div className='flex items-center gap-2 pt-1'>
                <Button asChild variant='outline' size='sm'>
                  <a
                    href={migrationUrl}
                    target='_blank'
                    rel='noopener noreferrer'
                  >
                    {t('watchdog.openInNewAddress')}
                    <ExternalLink className='h-3.5 w-3.5' />
                  </a>
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        )}

        {isExpired && (
          <Alert variant='destructive'>
            <AlertTriangle className='h-4 w-4' />
            <AlertTitle>{t('watchdog.expiredTitle')}</AlertTitle>
            <AlertDescription className='text-xs'>
              {t('watchdog.expiredDesc')}
            </AlertDescription>
          </Alert>
        )}
      </div>

      <DialogFooter className='gap-2 sm:justify-between'>
        {isExpired ? (
          <Button
            className='w-full'
            variant='outline'
            onClick={() => {
              onOpenChange(false)
              onRolledBack()
            }}
          >
            {t('watchdog.closeAndRefresh')}
          </Button>
        ) : (
          <>
            <Button
              variant='outline'
              onClick={handleRollback}
              disabled={rollbackMutation.isPending || confirmMutation.isPending}
              className='text-destructive hover:bg-destructive/10'
            >
              {rollbackMutation.isPending ? (
                <Loader2 className='me-1.5 h-4 w-4 animate-spin' />
              ) : (
                <RotateCcw className='me-1.5 h-4 w-4' />
              )}
              {t('actions.discardRollback')}
            </Button>

            <Button
              onClick={handleConfirm}
              disabled={confirmMutation.isPending || rollbackMutation.isPending}
            >
              {confirmMutation.isPending ? (
                <Loader2 className='me-1.5 h-4 w-4 animate-spin' />
              ) : (
                <CheckCircle2 className='me-1.5 h-4 w-4' />
              )}
              {t('actions.confirmPersist')}
            </Button>
          </>
        )}
      </DialogFooter>
    </>
  )
}

export function WatchdogModal({
  open,
  onOpenChange,
  transaction,
  onConfirmed,
  onRolledBack,
}: WatchdogModalProps) {
  if (!transaction) return null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // 关闭状态交给内部逻辑或外部控制
        onOpenChange(next)
      }}
    >
      <DialogContent
        className='max-h-[90vh] overflow-y-auto sm:max-w-md'
        onPointerDownOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        <WatchdogModalBody
          key={transaction.transactionId || transaction.confirmToken}
          transaction={transaction}
          onOpenChange={onOpenChange}
          onConfirmed={onConfirmed}
          onRolledBack={onRolledBack}
        />
      </DialogContent>
    </Dialog>
  )
}
