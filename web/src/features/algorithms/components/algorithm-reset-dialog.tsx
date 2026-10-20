import { useState } from 'react'
import { AxiosError } from 'axios'
import { Copy, LifeBuoy, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Algorithm } from '../data/schema'
import { useResetAlgorithm } from '../hooks/use-algorithms'

interface AlgorithmResetDialogProps {
  algorithm: Algorithm | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 熔断复位弹窗。
 *
 * 采用规范 §2.5 的「模态留存级」反馈：重置失败（409 `ALGORITHM_NOT_IN_CRASH_LOOP`
 * 或 500）时弹窗保持打开，把服务端已本地化的 message 渲染成弹窗内的警示 Banner，
 * 而不是关闭弹窗后靠全局 Toast 一闪而过。对应地 `useResetAlgorithm` 声明了
 * `suppressGlobalToast`，避免同一错误出现 Banner + Toast 双重反馈。
 *
 * 弹窗同时承担「根因提示」职责：熔断本身不是故障，它是对反复崩溃的刹车，
 * 因此这里给出 runner 日志的检索线索与常见根因清单，避免用户 reset 完又立刻熔断。
 */
export function AlgorithmResetDialog({
  algorithm,
  open,
  onOpenChange,
}: AlgorithmResetDialogProps): React.JSX.Element | null {
  const { t } = useTranslation('algorithms')
  const resetMutation = useResetAlgorithm()
  const [failureMessage, setFailureMessage] = useState<string | null>(null)

  if (!algorithm) return null

  // 关闭即清空失败态，避免下一次打开残留上一次的 Banner。
  const handleOpenChange = (next: boolean) => {
    if (!next) setFailureMessage(null)
    onOpenChange(next)
  }

  const handleCopyId = async (id: string) => {
    // 不假定成功：copyToClipboard 会在非安全上下文（明文 HTTP 局域网访问）下
    // 走 execCommand 回退，两条路都失败时必须给失败反馈，不能谎报已复制。
    const copied = await copyToClipboard(id)
    if (copied) {
      toast.success(t('copyAlgorithmIdSuccess', '算法标识已复制到剪贴板'))
    } else {
      toast.error(t('copyAlgorithmIdFailed', '复制失败，请手动复制'))
    }
  }

  const handleConfirm = () => {
    setFailureMessage(null)
    resetMutation.mutate(algorithm.id, {
      onSuccess: () => {
        toast.success(t('resetSuccess', '已解除熔断，算法回到「已停用」状态'))
        handleOpenChange(false)
      },
      onError: (error: unknown) => {
        // api-client 的响应拦截器已把服务端已本地化的 message 覆写到 error.message。
        setFailureMessage(
          error instanceof AxiosError && error.message.trim().length > 0
            ? error.message
            : t('resetFailedFallback', '重置失败，请稍后重试。')
        )
      },
    })
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={handleOpenChange}
      title={t('resetDialogTitle', '算法已进入熔断状态')}
      desc={t(
        'resetDialogDesc',
        '{{name}} 反复崩溃，系统已停止自动重启。重置只会解除熔断并回到「已停用」，不会自动重启算法，也不会改动任务绑定。',
        { name: algorithm.name }
      )}
      confirmText={t('resetConfirm', '重置熔断')}
      cancelBtnText={t('cancel', '取消')}
      isLoading={resetMutation.isPending}
      handleConfirm={handleConfirm}
    >
      <div className='flex flex-col gap-3'>
        {failureMessage && (
          <Alert variant='destructive'>
            <TriangleAlert />
            <AlertTitle>{t('resetFailedTitle', '重置失败')}</AlertTitle>
            <AlertDescription>
              <p>{failureMessage}</p>
              <p>
                {t('resetFailedHint', '请确认该算法仍处于熔断状态，然后重试。')}
              </p>
            </AlertDescription>
          </Alert>
        )}

        <Alert>
          <LifeBuoy />
          <AlertTitle>{t('resetDialogChecklistTitle', '排查建议')}</AlertTitle>
          <AlertDescription>
            <ul className='list-disc space-y-1 ps-4'>
              <li>
                {t(
                  'resetDialogChecklistRunnerLog',
                  'runner 的 stdout / stderr 已转发进宿主日志（字段 algorithm={{id}}），请先在那里定位崩溃原因。',
                  { id: algorithm.id }
                )}
              </li>
              <li>
                {t(
                  'resetDialogChecklistOperators',
                  '核对模型与算子是否被目标平台支持（参考 RKNN / CANN 算子支持列表）。'
                )}
              </li>
              <li>
                {t(
                  'resetDialogChecklistRuntime',
                  '比对 SDK、驱动与 ABI 版本是否与该算法包的构建工具链一致。'
                )}
              </li>
              <li>
                {t(
                  'resetDialogChecklistMemory',
                  '检查 NPU 侧 DMA / CMA 内存是否充足。'
                )}
              </li>
            </ul>
          </AlertDescription>
        </Alert>

        <div className='flex items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2'>
          <div className='flex min-w-0 flex-col'>
            <span className='text-xs text-muted-foreground'>
              {t('resetDialogIdentifier', '算法标识')}
            </span>
            <span className='truncate font-mono text-xs text-foreground select-all'>
              {algorithm.id}
            </span>
          </div>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            className='h-7 w-7 shrink-0'
            onClick={() => void handleCopyId(algorithm.id)}
            title={t('copyAlgorithmId', '复制算法标识')}
            aria-label={t('copyAlgorithmId', '复制算法标识')}
          >
            <Copy className='h-3.5 w-3.5' />
          </Button>
        </div>
      </div>
    </ConfirmDialog>
  )
}
