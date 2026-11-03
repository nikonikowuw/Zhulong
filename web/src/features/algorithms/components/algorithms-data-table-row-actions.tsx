import { type Row } from '@tanstack/react-table'
import {
  FileText,
  FlaskConical,
  MoreHorizontal,
  RotateCcw,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type Algorithm } from '../data/schema'
import { useAlgorithmsContext } from './algorithms-provider'

interface AlgorithmsDataTableRowActionsProps {
  row: Row<Algorithm>
}

export function AlgorithmsDataTableRowActions({
  row,
}: AlgorithmsDataTableRowActionsProps) {
  const { t } = useTranslation('algorithms')
  const { setOpen, setCurrentRow } = useAlgorithmsContext()
  const algo = row.original

  const isEnabled = algo.status === 'ACTIVE' || algo.status === 'RUNNING'
  const isCrashLoop = algo.status === 'CRASH_LOOP'
  const isTrialAllowed = algo.status === 'DISABLED'

  const handleTrial = (e?: React.SyntheticEvent) => {
    e?.stopPropagation()
    setCurrentRow(algo)
    setOpen('trial')
  }

  const handleDetail = (e?: React.SyntheticEvent) => {
    e?.stopPropagation()
    setCurrentRow(algo)
    setOpen('detail')
  }

  const handleReset = (e?: React.SyntheticEvent) => {
    e?.stopPropagation()
    setCurrentRow(algo)
    setOpen('reset')
  }

  const handleDelete = (e?: React.SyntheticEvent) => {
    e?.stopPropagation()
    setCurrentRow(algo)
    setOpen('delete')
  }

  return (
    <div
      className='flex items-center justify-end gap-1'
      data-prevent-row-click
      onClick={(e) => e.stopPropagation()}
    >
      {isCrashLoop && (
        <Button
          variant='outline'
          size='sm'
          className='h-8 gap-1.5 text-xs'
          onClick={handleReset}
        >
          <RotateCcw className='h-3.5 w-3.5' />
          <span>{t('resetCrashLoop', '重置熔断')}</span>
        </Button>
      )}

      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            variant='ghost'
            className='flex size-8 p-0 data-[state=open]:bg-muted'
            aria-label={t('thActions', '操作')}
          >
            <MoreHorizontal className='size-4' />
            <span className='sr-only'>{t('thActions', '操作')}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align='end'
          className='w-40'
          onClick={(e) => e.stopPropagation()}
        >
          <DropdownMenuItem onClick={handleDetail}>
            <FileText className='me-2 size-4' />
            {t('viewDetails', '查看详情')}
          </DropdownMenuItem>
          <DropdownMenuSeparator />

          {isCrashLoop && (
            <>
              <DropdownMenuItem onClick={handleReset}>
                <RotateCcw className='me-2 size-4' />
                {t('resetCrashLoop', '重置熔断')}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
            </>
          )}

          <DropdownMenuItem disabled={!isTrialAllowed} onClick={handleTrial}>
            <FlaskConical className='me-2 size-4' />
            {t('trialAlgorithm', '效果试跑')}
          </DropdownMenuItem>
          <DropdownMenuSeparator />

          <DropdownMenuItem
            className='text-destructive focus:text-destructive'
            disabled={isEnabled}
            onClick={handleDelete}
          >
            <Trash2 className='me-2 size-4' />
            {t('deleteAlgorithm', '卸载算法包')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
