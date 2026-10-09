import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Activity, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { createPingFormSchema, type PingFormValues } from '../data/schema'
import { usePingMutation } from '../hooks/use-network'

interface PingDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultTarget?: string
}

export function PingDialog({
  open,
  onOpenChange,
  defaultTarget = '8.8.8.8',
}: PingDialogProps) {
  const { t } = useTranslation('network')
  const pingMutation = usePingMutation()

  const schema = useMemo(() => createPingFormSchema(t), [t])

  const form = useForm<PingFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      target: defaultTarget,
    },
  })

  const resetPing = pingMutation.reset

  useEffect(() => {
    if (open && defaultTarget) {
      form.setValue('target', defaultTarget)
      resetPing()
    }
  }, [open, defaultTarget, form, resetPing])

  const onSubmit = (values: PingFormValues) => {
    pingMutation.mutate(values.target)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-md'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Activity className='h-5 w-5 text-primary' />
            <span>{t('ping.dialogTitle')}</span>
          </DialogTitle>
          <DialogDescription>{t('ping.dialogDesc')}</DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
            <FormField
              control={form.control}
              name='target'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('ping.targetLabel')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('ping.targetPlaceholder')}
                      className='font-mono'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription className='text-xs'>
                    {t('ping.targetDesc')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            {pingMutation.isPending && (
              <div className='flex items-center justify-center gap-2 rounded-lg border bg-muted/40 p-4 text-sm text-muted-foreground'>
                <Loader2 className='h-4 w-4 animate-spin' />
                <span>{t('ping.probingMsg')}</span>
              </div>
            )}

            {pingMutation.data && (
              <div
                className={`flex items-center justify-between rounded-lg border p-3.5 text-sm ${
                  pingMutation.data.reachable
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-300'
                    : 'border-destructive/30 bg-destructive/10 text-destructive'
                }`}
              >
                <div className='flex items-center gap-2'>
                  {pingMutation.data.reachable ? (
                    <CheckCircle2 className='h-4 w-4 text-emerald-600 dark:text-emerald-400' />
                  ) : (
                    <XCircle className='h-4 w-4 text-destructive' />
                  )}
                  <span className='font-medium'>
                    {pingMutation.data.reachable
                      ? t('ping.success')
                      : t('ping.failed')}
                  </span>
                </div>

                {pingMutation.data.reachable && (
                  <Badge variant='outline' className='font-mono text-xs'>
                    {t('ping.rtt')}: {pingMutation.data.rttMs.toFixed(2)} ms
                  </Badge>
                )}
              </div>
            )}

            {pingMutation.isError && (
              <div className='rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive'>
                {t('ping.probeError', { msg: pingMutation.error.message })}
              </div>
            )}

            <DialogFooter className='pt-2'>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
              >
                {t('actions.close')}
              </Button>
              <Button type='submit' disabled={pingMutation.isPending}>
                {pingMutation.isPending && (
                  <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                )}
                {pingMutation.isPending
                  ? t('actions.probing')
                  : t('actions.startPing')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
