import { useEffect } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import {
  type ApplyResponse,
  type InterfaceConfigPayload,
  type InterfaceInfo,
} from '../api/network-api'
import {
  interfaceFormSchema,
  parseDnsString,
  type InterfaceFormValues,
} from '../data/schema'
import { useApplyConfigMutation } from '../hooks/use-network'

interface InterfaceEditDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  iface: InterfaceInfo | null
  onSuccess: (applyResp: ApplyResponse) => void
}

function parseCIDR(cidr?: string): { ip: string; mask: string } {
  if (!cidr) return { ip: '', mask: '255.255.255.0' }
  const parts = cidr.split('/')
  const ip = parts[0] || ''
  const prefix = parseInt(parts[1] || '24', 10)

  if (isNaN(prefix) || prefix < 0 || prefix > 32) {
    return { ip, mask: '255.255.255.0' }
  }

  const maskNum = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0
  const mask = [
    (maskNum >>> 24) & 255,
    (maskNum >>> 16) & 255,
    (maskNum >>> 8) & 255,
    maskNum & 255,
  ].join('.')

  return { ip, mask }
}

export function InterfaceEditDialog({
  open,
  onOpenChange,
  iface,
  onSuccess,
}: InterfaceEditDialogProps) {
  const applyMutation = useApplyConfigMutation()

  const form = useForm<InterfaceFormValues>({
    resolver: zodResolver(interfaceFormSchema),
    defaultValues: {
      mode: 'dhcp',
      ipAddress: '',
      subnetMask: '255.255.255.0',
      gateway: '',
      dns: '',
      setDefault: false,
    },
  })

  const mode = useWatch({ control: form.control, name: 'mode' })

  useEffect(() => {
    if (iface) {
      const { ip, mask } = parseCIDR(iface.ipAddresses[0])
      form.reset({
        mode: iface.mode === 'static' ? 'static' : 'dhcp',
        ipAddress: ip,
        subnetMask: mask,
        gateway: iface.gateway || '',
        dns: iface.dns ? iface.dns.join(', ') : '',
        setDefault: iface.isDefaultGw || false,
      })
    }
  }, [iface, form])

  const onSubmit = async (values: InterfaceFormValues) => {
    if (!iface) return

    const payload: InterfaceConfigPayload = {
      mode: values.mode,
      setDefault: values.setDefault,
      dns: parseDnsString(values.dns),
    }

    if (values.mode === 'static') {
      payload.ipAddress = values.ipAddress?.trim()
      payload.subnetMask = values.subnetMask?.trim()
      if (values.gateway?.trim()) {
        payload.gateway = values.gateway.trim()
      }
    }

    try {
      const res = await applyMutation.mutateAsync({
        name: iface.name,
        payload,
      })
      toast.success(`网卡 ${iface.name} 配置已下发，进入两阶段试运行`)
      onOpenChange(false)
      onSuccess(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '配置应用失败'
      toast.error(msg)
    }
  }

  if (!iface) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <span>配置网卡：{iface.name}</span>
            <span className='font-mono text-xs text-muted-foreground'>
              ({iface.mac})
            </span>
          </DialogTitle>
          <DialogDescription>
            修改接口 IP
            分配模式、静态地址及路由出口。两阶段看门狗将确保配置安全。
          </DialogDescription>
        </DialogHeader>

        {iface.isCurrent && (
          <Alert
            variant='destructive'
            className='border-amber-500/50 bg-amber-500/10 text-amber-800 dark:text-amber-300'
          >
            <AlertTriangle className='h-4 w-4 text-amber-600 dark:text-amber-400' />
            <AlertTitle className='font-semibold'>高风险操作提示</AlertTitle>
            <AlertDescription className='text-xs'>
              此接口为当前管理面板进站网卡。若修改了 IP
              地址，当前连接将立即重构，并在 60
              秒内等待您在新地址完成确认，否则系统将自动回滚恢复原网络！
            </AlertDescription>
          </Alert>
        )}

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
            <FormField
              control={form.control}
              name='mode'
              render={({ field }) => (
                <FormItem className='space-y-2'>
                  <FormLabel>IP 获取方式</FormLabel>
                  <FormControl>
                    <RadioGroup
                      onValueChange={field.onChange}
                      value={field.value}
                      className='grid grid-cols-2 gap-4'
                    >
                      <div>
                        <RadioGroupItem
                          value='dhcp'
                          id='mode-dhcp'
                          className='peer sr-only'
                        />
                        <label
                          htmlFor='mode-dhcp'
                          className='flex cursor-pointer flex-col items-center justify-between rounded-md border-2 border-muted bg-popover p-3 peer-data-[state=checked]:border-primary hover:bg-accent hover:text-accent-foreground [&:has([data-state=checked])]:border-primary'
                        >
                          <span className='text-sm font-medium'>
                            DHCP (自动获取)
                          </span>
                          <span className='mt-0.5 text-xs text-muted-foreground'>
                            由路由器动态指派
                          </span>
                        </label>
                      </div>

                      <div>
                        <RadioGroupItem
                          value='static'
                          id='mode-static'
                          className='peer sr-only'
                        />
                        <label
                          htmlFor='mode-static'
                          className='flex cursor-pointer flex-col items-center justify-between rounded-md border-2 border-muted bg-popover p-3 peer-data-[state=checked]:border-primary hover:bg-accent hover:text-accent-foreground [&:has([data-state=checked])]:border-primary'
                        >
                          <span className='text-sm font-medium'>
                            Static (静态 IP)
                          </span>
                          <span className='mt-0.5 text-xs text-muted-foreground'>
                            手动指定固定地址
                          </span>
                        </label>
                      </div>
                    </RadioGroup>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {mode === 'static' && (
              <div className='space-y-3.5 rounded-lg border bg-muted/20 p-3.5'>
                <FormField
                  control={form.control}
                  name='ipAddress'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='text-xs'>IPv4 地址 *</FormLabel>
                      <FormControl>
                        <Input
                          placeholder='例如: 192.168.1.100'
                          className='font-mono'
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='subnetMask'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='text-xs'>子网掩码 *</FormLabel>
                      <FormControl>
                        <Input
                          placeholder='例如: 255.255.255.0'
                          className='font-mono'
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='gateway'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='text-xs'>默认网关 (可选)</FormLabel>
                      <FormControl>
                        <Input
                          placeholder='例如: 192.168.1.1'
                          className='font-mono'
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            )}

            <FormField
              control={form.control}
              name='dns'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>DNS 服务器</FormLabel>
                  <FormControl>
                    <Input
                      placeholder='例如: 8.8.8.8, 114.114.114.114'
                      className='font-mono'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription className='text-xs'>
                    多个 DNS 地址之间可用逗号或空格分隔
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='setDefault'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-3 shadow-xs'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-sm'>设为全局默认网关</FormLabel>
                    <FormDescription className='text-xs'>
                      系统所有未经指定的外网流量将由此网卡路由出口
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <DialogFooter className='pt-2'>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
                disabled={applyMutation.isPending}
              >
                取消
              </Button>
              <Button type='submit' disabled={applyMutation.isPending}>
                {applyMutation.isPending && (
                  <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                )}
                应用配置 (进入试运行)
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
