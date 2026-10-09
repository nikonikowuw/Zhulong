import { useEffect, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  ExternalLink,
  Loader2,
  RotateCcw,
} from 'lucide-react'
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
      toast.success('网络配置已成功确认并永久固化！')
      onOpenChange(false)
      onConfirmed()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '确认配置失败'
      toast.error(msg)
    }
  }

  const handleRollback = async () => {
    try {
      await rollbackMutation.mutateAsync(transaction.confirmToken)
      toast.info('网络配置已安全回滚至上一版本')
      onOpenChange(false)
      onRolledBack()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '执行回滚失败'
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
          <span>网络配置两阶段看门狗确认</span>
        </DialogTitle>
        <DialogDescription>
          新配置已下发并在试运行中。系统处于防失联看门狗保护状态。
        </DialogDescription>
      </DialogHeader>

      <div className='space-y-4 py-2'>
        {/* 倒计时看板 */}
        <div className='space-y-2 rounded-xl border bg-muted/40 p-4 text-center'>
          <div className='flex items-center justify-between text-xs text-muted-foreground'>
            <span>看门狗自动回滚剩余时间</span>
            <span className='font-mono text-sm font-bold text-foreground'>
              {timeLeft} 秒
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
            若在倒计时结束前未完成确认，边缘宿主机将自动回滚恢复原网络配置以防失联。
          </div>
        </div>

        {/* 迁移提示 */}
        {isMigrating && !isExpired && (
          <Alert className='border-blue-500/50 bg-blue-500/10 text-blue-900 dark:text-blue-300'>
            <AlertTriangle className='h-4 w-4 text-blue-600 dark:text-blue-400' />
            <AlertTitle className='font-semibold'>管理地址已变更</AlertTitle>
            <AlertDescription className='space-y-2 text-xs'>
              <p>
                当前访问网卡 IP
                已调整，您在当前界面的网络连接可能会中断。请立即跳转至新地址访问系统并在新窗口完成确认：
              </p>
              <div className='flex items-center gap-2 pt-1'>
                <a
                  href={migrationUrl}
                  target='_blank'
                  rel='noopener noreferrer'
                  className='inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white shadow-xs hover:bg-blue-700'
                >
                  在新地址打开并确认
                  <ExternalLink className='h-3.5 w-3.5' />
                </a>
              </div>
            </AlertDescription>
          </Alert>
        )}

        {isExpired && (
          <Alert variant='destructive'>
            <AlertTriangle className='h-4 w-4' />
            <AlertTitle>试运行倒计时已超时</AlertTitle>
            <AlertDescription className='text-xs'>
              看门狗已自动触发回滚流程，宿主机网络正在恢复至旧配置。请稍后刷新或检查设备连接。
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
            关闭并刷新网络状态
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
                <Loader2 className='mr-1.5 h-4 w-4 animate-spin' />
              ) : (
                <RotateCcw className='mr-1.5 h-4 w-4' />
              )}
              放弃并立即回滚
            </Button>

            <Button
              onClick={handleConfirm}
              disabled={confirmMutation.isPending || rollbackMutation.isPending}
              className='bg-emerald-600 text-white hover:bg-emerald-700'
            >
              {confirmMutation.isPending ? (
                <Loader2 className='mr-1.5 h-4 w-4 animate-spin' />
              ) : (
                <CheckCircle2 className='mr-1.5 h-4 w-4' />
              )}
              确认网络正常 (固化生效)
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
        className='sm:max-w-md'
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
