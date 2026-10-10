import { useEffect, useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
  createInterfaceFormSchema,
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
  const { t } = useTranslation('network')
  const applyMutation = useApplyConfigMutation()

  const schema = useMemo(() => createInterfaceFormSchema(t), [t])

  const form = useForm<InterfaceFormValues>({
    resolver: zodResolver(schema),
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
      toast.success(t('editDialog.applySuccess', { name: iface.name }))
      onOpenChange(false)
      onSuccess(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('errors.applyFailed')
      toast.error(msg)
    }
  }

  if (!iface) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[90vh] flex-col gap-0 p-0 sm:max-w-lg'>
        <DialogHeader className='flex-none border-b px-6 py-4'>
          <DialogTitle className='flex items-center gap-2'>
            <span>{t('editDialog.title', { name: iface.name })}</span>
            <span className='font-mono text-xs text-muted-foreground'>
              ({iface.mac})
            </span>
          </DialogTitle>
          <DialogDescription className='text-xs'>
            {t('editDialog.description')}
          </DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(onSubmit)}
            className='flex min-h-0 flex-1 flex-col'
          >
            <div className='flex-1 space-y-4 overflow-y-auto px-6 py-4'>
              {iface.isCurrent && (
                <Alert className='border-amber-500/50 bg-amber-500/10 px-3 py-2.5 text-amber-900 dark:text-amber-300'>
                  <AlertTriangle className='h-4 w-4 text-amber-600 dark:text-amber-400' />
                  <AlertTitle className='text-xs font-semibold'>
                    {t('editDialog.highRiskTitle')}
                  </AlertTitle>
                  <AlertDescription className='text-xs'>
                    {t('editDialog.highRiskDesc')}
                  </AlertDescription>
                </Alert>
              )}

              <FormField
                control={form.control}
                name='mode'
                render={({ field }) => (
                  <FormItem className='space-y-2'>
                    <FormLabel>{t('editDialog.ipMode')}</FormLabel>
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
                            className='flex cursor-pointer flex-col items-center justify-between rounded-md border-2 border-muted bg-popover p-2.5 peer-data-[state=checked]:border-primary hover:bg-accent hover:text-accent-foreground [&:has([data-state=checked])]:border-primary'
                          >
                            <span className='text-sm font-medium'>
                              {t('editDialog.dhcpTitle')}
                            </span>
                            <span className='mt-0.5 text-xs text-muted-foreground'>
                              {t('editDialog.dhcpDesc')}
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
                            className='flex cursor-pointer flex-col items-center justify-between rounded-md border-2 border-muted bg-popover p-2.5 peer-data-[state=checked]:border-primary hover:bg-accent hover:text-accent-foreground [&:has([data-state=checked])]:border-primary'
                          >
                            <span className='text-sm font-medium'>
                              {t('editDialog.staticTitle')}
                            </span>
                            <span className='mt-0.5 text-xs text-muted-foreground'>
                              {t('editDialog.staticDesc')}
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
                <div className='space-y-3 rounded-md border bg-muted/20 p-3'>
                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='ipAddress'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel className='text-xs'>
                            {t('editDialog.ipv4Label')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              placeholder={t('editDialog.ipv4Placeholder')}
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
                          <FormLabel className='text-xs'>
                            {t('editDialog.maskLabel')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              placeholder={t('editDialog.maskPlaceholder')}
                              className='font-mono'
                              {...field}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>

                  <FormField
                    control={form.control}
                    name='gateway'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel className='text-xs'>
                          {t('editDialog.gwLabel')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t('editDialog.gwPlaceholder')}
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
                    <FormLabel>{t('editDialog.dnsLabel')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('editDialog.dnsPlaceholder')}
                        className='font-mono'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription className='text-xs'>
                      {t('editDialog.dnsDesc')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='setDefault'
                render={({ field }) => (
                  <FormItem className='flex flex-row items-center justify-between rounded-md border p-3 shadow-xs'>
                    <div className='space-y-0.5'>
                      <FormLabel className='text-sm'>
                        {t('editDialog.defaultGwLabel')}
                      </FormLabel>
                      <FormDescription className='text-xs'>
                        {t('editDialog.defaultGwDesc')}
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
            </div>

            <DialogFooter className='flex-none gap-2 border-t bg-muted/20 px-6 py-3 sm:justify-end'>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
                disabled={applyMutation.isPending}
              >
                {t('actions.cancel')}
              </Button>
              <Button type='submit' disabled={applyMutation.isPending}>
                {applyMutation.isPending && (
                  <Loader2 className='me-2 h-4 w-4 animate-spin' />
                )}
                {applyMutation.isPending
                  ? t('actions.applying')
                  : t('actions.apply')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}
