import { useState } from 'react'
import { AlertTriangle, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { useClearAuditLogs } from '../hooks/use-audit-logs'
import { useAuditContext } from './AuditProvider'

export function AuditClearDialog() {
  const { t } = useTranslation('audit')
  const { isClearOpen, setIsClearOpen } = useAuditContext()
  const [rangeType, setRangeType] = useState<'all' | '7days' | '30days'>(
    '7days'
  )
  const clearMutation = useClearAuditLogs()

  const handleConfirm = async () => {
    let before: string | undefined

    if (rangeType === '7days') {
      const d = new Date()
      d.setDate(d.getDate() - 7)
      before = d.toISOString()
    } else if (rangeType === '30days') {
      const d = new Date()
      d.setDate(d.getDate() - 30)
      before = d.toISOString()
    }

    try {
      const res = await clearMutation.mutateAsync({ before })
      toast.success(t('toast.clearSuccess', { count: res.cleared }))
      setIsClearOpen(false)
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : String(err)
      toast.error(t('toast.clearFailed', { error: errMsg }))
    }
  }

  return (
    <Dialog open={isClearOpen} onOpenChange={setIsClearOpen}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader className='text-start'>
          <DialogTitle className='flex items-center gap-2 text-destructive'>
            <AlertTriangle className='h-5 w-5 shrink-0' />
            {t('dialog.clearTitle')}
          </DialogTitle>
          <DialogDescription className='pt-2 leading-normal'>
            {t('dialog.clearDesc')}
          </DialogDescription>
        </DialogHeader>

        <div className='py-3'>
          <Label className='mb-3 block text-sm font-medium text-foreground'>
            {t('dialog.clearRange')}
          </Label>

          <RadioGroup
            value={rangeType}
            onValueChange={(val) =>
              setRangeType(val as 'all' | '7days' | '30days')
            }
            className='gap-2.5'
          >
            <div className='flex items-center gap-2 rounded-lg border border-border p-3 hover:bg-muted/30'>
              <RadioGroupItem value='7days' id='clear-7days' />
              <Label
                htmlFor='clear-7days'
                className='flex-1 cursor-pointer text-xs font-normal'
              >
                {t('dialog.clearOlderThan7Days')}
              </Label>
            </div>

            <div className='flex items-center gap-2 rounded-lg border border-border p-3 hover:bg-muted/30'>
              <RadioGroupItem value='30days' id='clear-30days' />
              <Label
                htmlFor='clear-30days'
                className='flex-1 cursor-pointer text-xs font-normal'
              >
                {t('dialog.clearOlderThan30Days')}
              </Label>
            </div>

            <div className='flex items-center gap-2 rounded-lg border border-destructive/20 bg-destructive/5 p-3 hover:bg-destructive/10'>
              <RadioGroupItem value='all' id='clear-all' />
              <Label
                htmlFor='clear-all'
                className='flex-1 cursor-pointer text-xs font-medium text-destructive'
              >
                {t('dialog.clearAll')}
              </Label>
            </div>
          </RadioGroup>
        </div>

        <DialogFooter className='gap-2 sm:gap-0'>
          <DialogClose asChild>
            <Button variant='outline' disabled={clearMutation.isPending}>
              {t('dialog.cancel')}
            </Button>
          </DialogClose>
          <Button
            variant='destructive'
            onClick={handleConfirm}
            disabled={clearMutation.isPending}
            className='gap-1.5'
          >
            <Trash2 className='h-4 w-4' />
            {t('dialog.confirmClear')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
