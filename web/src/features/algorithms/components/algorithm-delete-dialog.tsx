import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Algorithm } from '../data/schema'
import { useDeleteAlgorithm } from '../hooks/use-algorithms'

interface AlgorithmDeleteDialogProps {
  algorithm: Algorithm | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function AlgorithmDeleteDialog({
  algorithm,
  open,
  onOpenChange,
}: AlgorithmDeleteDialogProps): React.JSX.Element | null {
  const { t } = useTranslation('algorithms')
  const deleteMutation = useDeleteAlgorithm()

  if (!algorithm) return null

  const handleConfirm = () => {
    deleteMutation.mutate(algorithm.id, {
      onSuccess: () => {
        onOpenChange(false)
      },
    })
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('deleteConfirmTitle', '确认卸载该算法包？')}
      desc={t(
        'deleteConfirmDesc',
        '将物理移除算法包 {{name}} (v{{version}}) 及其权重模型文件。若有正在使用该算法的分析任务，请先将其解绑。',
        {
          name: algorithm.name,
          version: algorithm.version,
        }
      )}
      confirmText={t('confirmDelete', '确认卸载')}
      cancelBtnText={t('cancel', '取消')}
      destructive
      isLoading={deleteMutation.isPending}
      handleConfirm={handleConfirm}
    />
  )
}
