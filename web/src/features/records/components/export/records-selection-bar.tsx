import { Download, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

export interface RecordsSelectionBarProps {
  selectedCount: number
  onExportSelected: () => void
  onClearSelection: () => void
}

export function RecordsSelectionBar({
  selectedCount,
  onExportSelected,
  onClearSelection,
}: RecordsSelectionBarProps) {
  const { t } = useTranslation('records')

  if (selectedCount <= 0) {
    return null
  }

  return (
    <div
      role='region'
      aria-label={t('selection.ariaLabel', { defaultValue: '批量多选操作' })}
      className='fixed bottom-6 left-1/2 z-40 flex -translate-x-1/2 animate-in items-center gap-3 rounded-full border border-border/80 bg-background/90 px-4 py-2 shadow-xl backdrop-blur-md duration-200 slide-in-from-bottom-5 fade-in'
    >
      <div className='flex items-center gap-2 text-xs font-medium'>
        <span className='flex size-2 animate-pulse rounded-full bg-primary' />
        {/* live region 只包计数：挂在容器上会让读屏每次变化重复朗读所有按钮 */}
        <span aria-live='polite'>
          {t('selection.selectedCount', {
            count: selectedCount,
            defaultValue: `已选择 ${selectedCount} 项`,
          })}
        </span>
      </div>

      <div className='h-4 w-px bg-border' />

      <div className='flex items-center gap-1.5'>
        <Button
          type='button'
          size='sm'
          className='h-7 gap-1.5 rounded-full px-3 text-xs'
          onClick={onExportSelected}
        >
          <Download className='size-3.5' />
          <span>
            {t('selection.exportSelected', {
              defaultValue: '导出选中项',
            })}
          </span>
        </Button>

        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-7 gap-1 rounded-full px-2.5 text-xs text-muted-foreground hover:text-foreground'
          onClick={onClearSelection}
        >
          <X className='size-3.5' />
          <span>{t('selection.clearSelection', { defaultValue: '清空' })}</span>
        </Button>
      </div>
    </div>
  )
}
