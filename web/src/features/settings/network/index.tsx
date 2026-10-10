import { useMemo, useState } from 'react'
import {
  Activity,
  AlertTriangle,
  Clock,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ContentSection } from '../components/content-section'
import { type ApplyResponse, type InterfaceInfo } from './api/network-api'
import { InterfaceEditDialog } from './components/interface-edit-dialog'
import { InterfaceTable } from './components/interface-table'
import { PingDialog } from './components/ping-dialog'
import {
  WatchdogModal,
  type WatchdogTransactionData,
} from './components/watchdog-modal'
import { useNetworkInterfaces, useNetworkStatus } from './hooks/use-network'

export function SettingsNetwork() {
  const { t } = useTranslation('network')
  const interfacesQuery = useNetworkInterfaces()
  const statusQuery = useNetworkStatus()

  // 弹窗状态
  const [editingInterface, setEditingInterface] =
    useState<InterfaceInfo | null>(null)
  const [isEditOpen, setIsEditOpen] = useState(false)

  const [isPingOpen, setIsPingOpen] = useState(false)
  const [pingTarget, setPingTarget] = useState<string>('8.8.8.8')

  // 本地已提交的试运行事务
  const [appliedTransaction, setAppliedTransaction] =
    useState<WatchdogTransactionData | null>(null)

  // 处理 URL 中的 confirm_token 参数（例如跨 IP 迁移跳转回来时）
  const urlTransaction = useMemo<WatchdogTransactionData | null>(() => {
    try {
      const searchParams = new URLSearchParams(window.location.search)
      const token = searchParams.get('confirm_token')
      if (token) {
        return {
          transactionId: 'pending',
          timeoutSec: 60,
          targetUrl: window.location.href,
          confirmToken: token,
        }
      }
    } catch {
      // 忽略参数解析异常
    }
    return null
  }, [])

  // 从服务端同步的活跃未决事务
  const serverTransaction = useMemo<WatchdogTransactionData | null>(() => {
    if (statusQuery.data && statusQuery.data.status === 'pending_confirm') {
      const s = statusQuery.data
      return {
        transactionId: s.transactionId,
        timeoutSec: s.timeoutSec || 60,
        targetUrl: s.targetUrl,
        confirmToken: s.confirmToken,
        expiresAt: s.expiresAt,
      }
    }
    return null
  }, [statusQuery.data])

  // 当前有效事务：优先本地刚下发的，其次服务端活跃的，最后 URL 携带的
  const activeTransaction =
    appliedTransaction || serverTransaction || urlTransaction

  // 模态框打开状态：如果从 URL 带来 token 则默认打开，否则按需打开
  const [isWatchdogOpen, setIsWatchdogOpen] = useState<boolean>(() =>
    Boolean(urlTransaction)
  )

  const handleEdit = (iface: InterfaceInfo) => {
    setEditingInterface(iface)
    setIsEditOpen(true)
  }

  const handlePing = (target?: string) => {
    setPingTarget(target || '8.8.8.8')
    setIsPingOpen(true)
  }

  const handleApplySuccess = (resp: ApplyResponse) => {
    setAppliedTransaction({
      transactionId: resp.transactionId,
      timeoutSec: resp.timeoutSec,
      targetUrl: resp.targetUrl,
      confirmToken: resp.confirmToken,
    })
    setIsWatchdogOpen(true)
  }

  const handleRefetchAll = () => {
    interfacesQuery.refetch()
    statusQuery.refetch()
  }

  const handleTransactionClosed = () => {
    setAppliedTransaction(null)
    setIsWatchdogOpen(false)
    handleRefetchAll()
  }

  const isPendingConfirm = Boolean(serverTransaction || appliedTransaction)
  const interfaceCount = interfacesQuery.data?.length ?? 0

  return (
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='max-w-5xl'
    >
      <div className='space-y-4'>
        {/* 工具栏与操作入口 */}
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='flex items-center gap-2 text-xs text-muted-foreground'>
            <ShieldCheck className='h-4 w-4 text-primary' />
            <span>{t('discoveredInterfaces', { count: interfaceCount })}</span>
          </div>

          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              onClick={() => handlePing('8.8.8.8')}
              className='gap-1.5'
            >
              <Activity className='h-3.5 w-3.5' />
              {t('actions.ping')}
            </Button>

            <Button
              variant='outline'
              size='sm'
              onClick={handleRefetchAll}
              disabled={interfacesQuery.isFetching || statusQuery.isFetching}
              className='gap-1.5'
            >
              <RefreshCw
                className={`h-3.5 w-3.5 ${
                  interfacesQuery.isFetching ? 'animate-spin' : ''
                }`}
              />
              {t('actions.refresh')}
            </Button>
          </div>
        </div>

        {/* 未决事务全局警示横幅 */}
        {isPendingConfirm && (
          <Alert className='border-amber-500/50 bg-amber-500/10 text-amber-900 dark:text-amber-300'>
            <AlertTriangle className='h-4 w-4 text-amber-600 dark:text-amber-400' />
            <AlertTitle className='flex items-center justify-between font-semibold'>
              <span>{t('watchdog.trialActiveTitle')}</span>
              <Button
                size='sm'
                variant='outline'
                onClick={() => setIsWatchdogOpen(true)}
                className='h-7 gap-1 border-amber-500/50 bg-background text-xs font-medium text-amber-700 hover:bg-amber-500/10 dark:text-amber-300'
              >
                <Clock className='h-3 w-3' />
                {t('actions.openConfirmPanel')}
              </Button>
            </AlertTitle>
            <AlertDescription className='text-xs'>
              {t('watchdog.trialActiveDesc')}
            </AlertDescription>
          </Alert>
        )}

        {/* 物理网卡表格视图 */}
        <InterfaceTable
          interfaces={interfacesQuery.data}
          isLoading={interfacesQuery.isLoading}
          isError={interfacesQuery.isError}
          error={interfacesQuery.error}
          onRefetch={handleRefetchAll}
          onEdit={handleEdit}
          onPing={handlePing}
        />

        {/* 网卡编辑配置弹窗 */}
        <InterfaceEditDialog
          open={isEditOpen}
          onOpenChange={setIsEditOpen}
          iface={editingInterface}
          onSuccess={handleApplySuccess}
        />

        {/* Ping 探测工具弹窗 */}
        <PingDialog
          open={isPingOpen}
          onOpenChange={setIsPingOpen}
          defaultTarget={pingTarget}
        />

        {/* 两阶段看门狗安全确认与回滚模态框 */}
        <WatchdogModal
          open={isWatchdogOpen}
          onOpenChange={setIsWatchdogOpen}
          transaction={activeTransaction}
          onConfirmed={handleTransactionClosed}
          onRolledBack={handleTransactionClosed}
        />
      </div>
    </ContentSection>
  )
}
