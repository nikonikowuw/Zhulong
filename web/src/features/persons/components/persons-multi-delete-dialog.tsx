import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { type Table } from '@tanstack/react-table'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { type Person } from '../data/schema'
import { PERSONS_QUERY_KEY, useDeletePerson } from '../hooks/use-persons'

type PersonsMultiDeleteDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  table: Table<Person>
}

/** 并发上限：100 行选中一次打出 100 个 DELETE 会把 SQLite 写锁和 HTTP 池一起打满。 */
const DELETE_CONCURRENCY = 6

/**
 * 有界并发执行。返回结果按输入顺序对齐，`Promise.allSettled` 的语义不变。
 */
async function mapWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  worker: (item: T) => Promise<R>
): Promise<PromiseSettledResult<R>[]> {
  const results: PromiseSettledResult<R>[] = Array.from(
    { length: items.length },
    () => ({ status: 'rejected', reason: new Error('not started') })
  )
  let cursor = 0

  const run = async () => {
    for (;;) {
      const index = cursor++
      if (index >= items.length) return
      try {
        results[index] = {
          status: 'fulfilled',
          value: await worker(items[index]),
        }
      } catch (err) {
        results[index] = { status: 'rejected', reason: err }
      }
    }
  }

  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, () => run())
  )
  return results
}

export function PersonsMultiDeleteDialog({
  open,
  onOpenChange,
  table,
}: PersonsMultiDeleteDialogProps) {
  const { t } = useTranslation('persons')
  const [value, setValue] = useState('')
  const [isDeleting, setIsDeleting] = useState(false)
  const queryClient = useQueryClient()
  const deleteMutation = useDeletePerson({
    suppressGlobalToast: true,
    suppressInvalidate: true,
  })

  const confirmWord = t('dialogs.confirmWord')
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedPersons = selectedRows.map((row) => row.original)

  const handleDelete = async () => {
    if (value.trim() !== confirmWord) {
      toast.error(t('dialogs.confirmWordMismatch'))
      return
    }

    setIsDeleting(true)
    try {
      const results = await mapWithConcurrency(
        selectedPersons,
        DELETE_CONCURRENCY,
        (p) => deleteMutation.mutateAsync(p.id)
      )
      const successCount = results.filter(
        (r) => r.status === 'fulfilled'
      ).length
      const failedCount = results.length - successCount

      if (successCount > 0) {
        void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      }

      if (failedCount === 0) {
        toast.success(
          t('notifications.batchDeleteSuccess', {
            count: selectedPersons.length,
          })
        )
      } else if (successCount > 0) {
        toast.warning(
          t('notifications.batchDeletePartial', {
            success: successCount,
            failed: failedCount,
          })
        )
      } else {
        toast.error(t('notifications.batchDeleteError'))
      }

      setValue('')
      table.resetRowSelection()
      onOpenChange(false)
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(val) => {
        if (!isDeleting) {
          setValue('')
          onOpenChange(val)
        }
      }}
      form='persons-multi-delete-form'
      disabled={value.trim() !== confirmWord || isDeleting}
      isLoading={isDeleting}
      destructive
      title={
        <span className='text-destructive'>
          <AlertTriangle
            className='me-1 inline-block stroke-destructive'
            size={18}
          />{' '}
          {t('dialogs.batchDeleteTitle', { count: selectedPersons.length })}
        </span>
      }
      desc={
        <form
          id='persons-multi-delete-form'
          onSubmit={(e) => {
            e.preventDefault()
            void handleDelete()
          }}
          className='space-y-4'
        >
          <p className='text-sm text-muted-foreground'>
            {t('dialogs.batchDeleteDesc', { count: selectedPersons.length })}
          </p>

          <Label className='my-4 flex flex-col items-start gap-1.5'>
            <span className='text-sm font-medium'>
              {t('dialogs.confirmDeletePrompt', { word: confirmWord })}
            </span>
            <Input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={confirmWord}
              disabled={isDeleting}
              autoFocus
            />
          </Label>
        </form>
      }
      confirmText={
        isDeleting ? (
          <span className='flex items-center gap-1.5'>
            <Loader2 className='size-4 animate-spin' />
            {t('actions.deleting')}
          </span>
        ) : (
          t('actions.batchDelete')
        )
      }
    />
  )
}
