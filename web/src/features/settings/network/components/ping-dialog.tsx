import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Activity, CheckCircle2, Loader2, XCircle } from 'lucide-react'
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
import { pingFormSchema, type PingFormValues } from '../data/schema'
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
  const pingMutation = usePingMutation()

  const form = useForm<PingFormValues>({
    resolver: zodResolver(pingFormSchema),
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
            <span>网络连通性探测 (Ping)</span>
          </DialogTitle>
          <DialogDescription>
            从宿主机边缘系统发起轻量 ICMP / Socket
            连通性测试，验证网关或外网可达性。
          </DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
            <FormField
              control={form.control}
              name='target'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>目标 IP 或域名</FormLabel>
                  <FormControl>
                    <Input
                      placeholder='例如: 192.168.1.1 或 8.8.8.8'
                      className='font-mono'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription className='text-xs'>
                    建议优先测试当前配置的默认网关，再测试公共 DNS 服务器
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            {pingMutation.isPending && (
              <div className='flex items-center justify-center gap-2 rounded-lg border bg-muted/40 p-4 text-sm text-muted-foreground'>
                <Loader2 className='h-4 w-4 animate-spin' />
                <span>正在向目标地址发送探测报文...</span>
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
                      ? '目标可达'
                      : '目标不可达 / 超时'}
                  </span>
                </div>

                {pingMutation.data.reachable && (
                  <Badge variant='outline' className='font-mono text-xs'>
                    RTT: {pingMutation.data.rttMs.toFixed(2)} ms
                  </Badge>
                )}
              </div>
            )}

            {pingMutation.isError && (
              <div className='rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive'>
                探测执行异常: {pingMutation.error.message}
              </div>
            )}

            <DialogFooter className='pt-2'>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
              >
                关闭
              </Button>
              <Button type='submit' disabled={pingMutation.isPending}>
                {pingMutation.isPending && (
                  <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                )}
                开始探测
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
