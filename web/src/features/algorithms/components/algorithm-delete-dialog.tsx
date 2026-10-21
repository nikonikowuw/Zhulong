import { useTranslation } from 'react-i18next'
import { AlertTriangle, ListChecks, ShieldCheck } from 'lucide-react'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { type Algorithm } from '../data/schema'
import { useDeletionImpact, useDeleteAlgorithm } from '../hooks/use-algorithms'

interface AlgorithmDeleteDialogProps {
  algorithm: Algorithm | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 卸载确认弹窗。
 *
 * 设计要点：卸载是**不可撤销**的，所以弹窗必须区分两类后果 ——
 *   1. 会真正失去的能力：任务解绑、算法包与模型文件被删；
 *   2. 不会丢失的东西：历史证据**保留**，只是失去 algorithmId 归属，
 *      algorithmName / algorithmVersion 快照仍在（迁移 000015/000016）。
 *
 * ⚠️ 绝不能用「将删除 N 条记录」这种文案。用户看到会以为证据没了，
 * 从而不敢卸载或误以为需要先备份。真实语义是「失去归属」。
 */
export function AlgorithmDeleteDialog({
  algorithm,
  open,
  onOpenChange,
}: AlgorithmDeleteDialogProps): React.JSX.Element | null {
  const { t } = useTranslation('algorithms')
  const deleteMutation = useDeleteAlgorithm()
  const impactQuery = useDeletionImpact(algorithm?.id ?? null, open)

  if (!algorithm) return null

  const impact = impactQuery.data
  const handleConfirm = () => {
    deleteMutation.mutate(algorithm.id, {
      onSuccess: () => {
        onOpenChange(false)
      },
    })
  }

  const description = (
    <div className='space-y-3'>
      <p>
        {t(
          'deleteConfirmDesc',
          '将物理移除算法包 {{name}} (v{{version}}) 及其权重模型文件。',
          { name: algorithm.name, version: algorithm.version }
        )}
      </p>

      {impactQuery.isPending && (
        <div className='space-y-2' aria-busy='true'>
          <Skeleton className='h-4 w-3/4' />
          <Skeleton className='h-4 w-1/2' />
        </div>
      )}

      {/* 影响面取不到时不能静默：用户有权知道提示不完整。 */}
      {impactQuery.isError && (
        <p className='flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs'>
          <AlertTriangle className='mt-0.5 size-3.5 shrink-0 text-amber-600' />
          {t(
            'impactUnavailable',
            '无法获取影响面统计，请谨慎确认后再卸载。'
          )}
        </p>
      )}

      {impact && (
        <div className='space-y-2 rounded-md border border-border bg-muted/30 p-3 text-xs'>
          {/* 会失去的：任务绑定 */}
          <div className='flex items-start gap-2'>
            <ListChecks className='mt-0.5 size-3.5 shrink-0 text-muted-foreground' />
            <span>
              {impact.boundTaskCount > 0
                ? t(
                    'impactTasks',
                    '将解绑 {{count}} 路分析任务，受影响的摄像机：{{cameras}}。任务本身保留，但会失去该算法。',
                    {
                      count: impact.boundTaskCount,
                      cameras: impact.affectedCameraIds.join('、'),
                    }
                  )
                : t('impactNoTasks', '当前没有分析任务绑定该算法。')}
            </span>
          </div>

          {/* 不会失去的：证据保留，只失去归属 */}
          {(impact.recognitionCount > 0 ||
            impact.alarmCount > 0 ||
            impact.captureCount > 0) && (
            <div className='flex items-start gap-2'>
              <ShieldCheck className='mt-0.5 size-3.5 shrink-0 text-emerald-600' />
              <span>
                {t(
                  'impactEvidenceKept',
                  '历史证据不会被删除：{{recognitions}} 条识别、{{alarms}} 条告警、{{captures}} 条抓拍将继续保留，并保留算法名称与版本快照，但无法再按该算法筛选。',
                  {
                    recognitions: impact.recognitionCount,
                    alarms: impact.alarmCount,
                    captures: impact.captureCount,
                  }
                )}
              </span>
            </div>
          )}

          {impact.requiresStop && (
            <div className='flex items-start gap-2'>
              <AlertTriangle className='mt-0.5 size-3.5 shrink-0 text-amber-600' />
              <span>
                {t(
                  'impactRequiresStop',
                  '该算法当前正在运行，卸载会先强制停止其进程。'
                )}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  )

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('deleteConfirmTitle', '确认卸载该算法包？')}
      desc={description}
      confirmText={t('confirmDelete', '确认卸载')}
      cancelBtnText={t('cancel', '取消')}
      destructive
      isLoading={deleteMutation.isPending}
      handleConfirm={handleConfirm}
    />
  )
}
