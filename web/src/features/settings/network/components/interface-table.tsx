import {
  Activity,
  AlertCircle,
  Network,
  RefreshCw,
  SlidersHorizontal,
} from 'lucide-react'
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
  if (isError) {
    return (
      <Alert variant='destructive' className='my-4'>
        <AlertCircle className='h-4 w-4' />
        <AlertTitle>获取网络接口失败</AlertTitle>
        <AlertDescription className='flex items-center justify-between'>
          <span>{error?.message || '无法连接至宿主系统网络服务'}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={onRefetch}
            className='ml-4 gap-1.5 bg-background text-foreground'
          >
            <RefreshCw className='h-3.5 w-3.5' />
            重试
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='rounded-md border bg-card'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-[140px]'>网络接口</TableHead>
            <TableHead className='w-[160px]'>物理状态 / MAC</TableHead>
            <TableHead className='w-[100px]'>模式</TableHead>
            <TableHead>IPv4 地址</TableHead>
            <TableHead>默认网关 / DNS</TableHead>
            <TableHead className='w-[140px] text-end'>操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <>
              {[1, 2].map((i) => (
                <TableRow key={i}>
                  <TableCell>
                    <Skeleton className='h-5 w-20' />
                  </TableCell>
                  <TableCell className='space-y-1.5'>
                    <Skeleton className='h-4 w-20' />
                    <Skeleton className='h-3 w-28' />
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
                    <Skeleton className='ml-auto h-8 w-16' />
                  </TableCell>
                </TableRow>
              ))}
            </>
          )}

          {!isLoading && (!interfaces || interfaces.length === 0) && (
            <TableRow>
              <TableCell
                colSpan={6}
                className='h-32 text-center text-muted-foreground'
              >
                <div className='flex flex-col items-center justify-center gap-2'>
                  <Network className='h-6 w-6 text-muted-foreground' />
                  <span>未探测到任何物理网络接口</span>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={onRefetch}
                    className='mt-1 h-7 text-xs'
                  >
                    <RefreshCw className='mr-1.5 h-3 w-3' />
                    重新检测
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          )}

          {!isLoading &&
            interfaces &&
            interfaces.map((iface) => {
              const primaryIp =
                iface.ipAddresses.length > 0
                  ? iface.ipAddresses[0]
                  : '未分配 IP'
              const extraIps = iface.ipAddresses.slice(1)

              return (
                <TableRow
                  key={iface.name}
                  data-testid={`interface-row-${iface.name}`}
                  className={iface.isCurrent ? 'bg-primary/[0.02]' : ''}
                >
                  {/* 接口名与当前管理网卡标识 */}
                  <TableCell className='font-medium'>
                    <div className='flex flex-col gap-1'>
                      <div className='flex items-center gap-2'>
                        <span className='font-semibold'>{iface.name}</span>
                        {iface.isDefaultGw && (
                          <Badge
                            variant='secondary'
                            className='border-blue-200 bg-blue-50 px-1.5 py-0 text-[10px] text-blue-700 dark:border-blue-800 dark:bg-blue-950 dark:text-blue-300'
                          >
                            默认
                          </Badge>
                        )}
                      </div>
                      {iface.isCurrent && (
                        <span className='text-[11px] text-amber-600 dark:text-amber-400'>
                          当前管理连接
                        </span>
                      )}
                    </div>
                  </TableCell>

                  {/* 物理状态与 MAC */}
                  <TableCell>
                    <div className='space-y-1'>
                      <Badge
                        variant={iface.linkUp ? 'default' : 'secondary'}
                        className={`text-[11px] ${
                          iface.linkUp
                            ? 'bg-emerald-600 hover:bg-emerald-600 dark:bg-emerald-600'
                            : ''
                        }`}
                      >
                        <span
                          className={`mr-1 inline-block h-1.5 w-1.5 rounded-full ${
                            iface.linkUp ? 'bg-white' : 'bg-muted-foreground'
                          }`}
                        />
                        {iface.linkUp ? 'Link Up' : 'Down'}
                      </Badge>
                      <div className='font-mono text-xs text-muted-foreground'>
                        {iface.mac || '—'}
                      </div>
                    </div>
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
                      <div className='font-mono text-[11px] text-muted-foreground'>
                        +{extraIps.join(', ')}
                      </div>
                    )}
                  </TableCell>

                  {/* 网关与 DNS */}
                  <TableCell>
                    <div className='space-y-0.5 text-xs'>
                      <div className='font-mono text-muted-foreground'>
                        网关: {iface.gateway || '—'}
                      </div>
                      <div className='max-w-[200px] truncate font-mono text-[11px] text-muted-foreground'>
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
                        title='连通性探测'
                        onClick={() =>
                          onPing(iface.gateway || primaryIp.split('/')[0])
                        }
                      >
                        <Activity className='h-4 w-4' />
                        <span className='sr-only'>探测连通性</span>
                      </Button>

                      <Button
                        variant='outline'
                        size='sm'
                        className='h-8 gap-1 px-2.5 text-xs'
                        onClick={() => onEdit(iface)}
                      >
                        <SlidersHorizontal className='h-3.5 w-3.5' />
                        配置
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
