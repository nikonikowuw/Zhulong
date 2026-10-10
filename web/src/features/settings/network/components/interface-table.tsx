import {
  Activity,
  AlertCircle,
  Network,
  RefreshCw,
  SlidersHorizontal,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type InterfaceInfo } from '../api/network-api'

interface InterfaceTableProps {
  interfaces?: InterfaceInfo[]
  isLoading: boolean
  isError: boolean
  error: Error | null
  onRefetch: () => void
  onEdit: (iface: InterfaceInfo) => void
  onPing: (target?: string) => void
}

export function InterfaceTable({
  interfaces,
  isLoading,
  isError,
  error,
  onRefetch,
  onEdit,
  onPing,
}: InterfaceTableProps) {
  const { t } = useTranslation('network')

  if (isError) {
    return (
      <Alert variant='destructive' className='my-4'>
        <AlertCircle className='h-4 w-4' />
        <AlertTitle>{t('errors.loadFailed')}</AlertTitle>
        <AlertDescription className='flex items-center justify-between'>
          <span>{error?.message || t('errors.connectionFailed')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={onRefetch}
            className='ms-4 gap-1.5 bg-background text-foreground'
          >
            <RefreshCw className='h-3.5 w-3.5' />
            {t('actions.retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='rounded-md border border-border bg-card'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-[180px]'>{t('table.interface')}</TableHead>
            <TableHead className='w-[160px]'>{t('table.status')}</TableHead>
            <TableHead className='w-[100px]'>{t('table.mode')}</TableHead>
            <TableHead>{t('table.ipv4')}</TableHead>
            <TableHead>{t('table.gatewayDns')}</TableHead>
            <TableHead className='w-[140px] text-end'>
              {t('table.operations')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading &&
            [1, 2].map((i) => (
              <TableRow key={i}>
                <TableCell className='space-y-1.5'>
                  <Skeleton className='h-4 w-20' />
                  <Skeleton className='h-3 w-28' />
                </TableCell>
                <TableCell>
                  <Skeleton className='h-5 w-24' />
                </TableCell>
                <TableCell>
                  <Skeleton className='h-5 w-14' />
                </TableCell>
                <TableCell>
                  <Skeleton className='h-4 w-28' />
                </TableCell>
                <TableCell className='space-y-1.5'>
                  <Skeleton className='h-4 w-24' />
                  <Skeleton className='h-3 w-32' />
                </TableCell>
                <TableCell className='text-end'>
                  <Skeleton className='ms-auto h-8 w-16' />
                </TableCell>
              </TableRow>
            ))}

          {!isLoading && (!interfaces || interfaces.length === 0) && (
            <TableRow>
              <TableCell
                colSpan={6}
                className='h-32 text-center text-muted-foreground'
              >
                <div className='flex flex-col items-center justify-center gap-2'>
                  <Network className='h-6 w-6 text-muted-foreground' />
                  <span>{t('table.empty')}</span>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={onRefetch}
                    className='mt-1 h-7 text-xs'
                  >
                    <RefreshCw className='me-1.5 h-3 w-3' />
                    {t('actions.refresh')}
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          )}

          {!isLoading &&
            interfaces?.map((iface) => {
              const primaryIp =
                iface.ipAddresses.length > 0
                  ? iface.ipAddresses[0]
                  : t('table.noIp')
              const extraIps = iface.ipAddresses.slice(1)

              return (
                <TableRow
                  key={iface.name}
                  data-testid={`interface-row-${iface.name}`}
                >
                  {/* 接口信息与访问入口 */}
                  <TableCell className='font-medium'>
                    <div className='flex flex-col gap-1'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <span className='font-semibold'>{iface.name}</span>
                        {iface.isCurrent && (
                          <Badge
                            variant='outline'
                            className='border-primary/30 bg-primary/5 px-1.5 py-0 text-xs font-normal text-primary'
                          >
                            {t('table.currentAccess')}
                          </Badge>
                        )}
                      </div>
                      <div className='font-mono text-xs font-normal text-muted-foreground'>
                        {iface.mac || '—'}
                      </div>
                    </div>
                  </TableCell>

                  {/* 链路状态 */}
                  <TableCell>
                    <Badge
                      variant='outline'
                      className={`gap-1.5 text-xs font-normal ${
                        iface.linkUp
                          ? 'border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200'
                          : 'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
                      }`}
                    >
                      <span
                        className={`inline-block h-1.5 w-1.5 rounded-full ${
                          iface.linkUp
                            ? 'bg-teal-700 dark:bg-teal-200'
                            : 'bg-muted-foreground/60'
                        }`}
                      />
                      {iface.linkUp
                        ? t('table.connected')
                        : t('table.disconnected')}
                    </Badge>
                  </TableCell>

                  {/* 分配模式 */}
                  <TableCell>
                    <Badge variant='outline' className='text-xs uppercase'>
                      {iface.mode}
                    </Badge>
                  </TableCell>

                  {/* IPv4 地址 */}
                  <TableCell>
                    <div className='font-mono text-xs font-medium text-foreground'>
                      {primaryIp}
                    </div>
                    {extraIps.length > 0 && (
                      <div className='font-mono text-xs text-muted-foreground'>
                        +{extraIps.join(', ')}
                      </div>
                    )}
                  </TableCell>

                  {/* 网关与 DNS */}
                  <TableCell>
                    <div className='space-y-0.5 text-xs'>
                      <div className='flex flex-wrap items-center gap-1.5'>
                        <span className='font-mono text-muted-foreground'>
                          {t('table.gateway')}: {iface.gateway || '—'}
                        </span>
                        {iface.isDefaultGw && (
                          <Badge
                            variant='secondary'
                            className='border-sky-300 bg-sky-200/40 px-1.5 py-0 text-xs font-normal text-sky-900 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-100'
                          >
                            {t('table.defaultGateway')}
                          </Badge>
                        )}
                      </div>
                      <div className='max-w-[200px] truncate font-mono text-xs text-muted-foreground'>
                        DNS: {iface.dns?.length ? iface.dns.join(', ') : '—'}
                      </div>
                    </div>
                  </TableCell>

                  {/* 操作 */}
                  <TableCell className='text-end'>
                    <div className='flex items-center justify-end gap-1.5'>
                      <Button
                        variant='ghost'
                        size='sm'
                        className='h-8 w-8 p-0 text-muted-foreground hover:text-foreground'
                        title={t('actions.ping')}
                        onClick={() =>
                          onPing(iface.gateway || primaryIp.split('/')[0])
                        }
                      >
                        <Activity className='h-4 w-4' />
                        <span className='sr-only'>{t('actions.ping')}</span>
                      </Button>

                      <Button
                        variant='outline'
                        size='sm'
                        className='h-8 gap-1 px-2.5 text-xs'
                        onClick={() => onEdit(iface)}
                      >
                        <SlidersHorizontal className='h-3.5 w-3.5' />
                        {t('actions.configure')}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
        </TableBody>
      </Table>
    </div>
  )
}
