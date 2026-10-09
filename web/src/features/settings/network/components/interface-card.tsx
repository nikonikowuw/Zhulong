import {
  Activity,
  AlertTriangle,
  Network,
  SlidersHorizontal,
} from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { type InterfaceInfo } from '../api/network-api'

interface InterfaceCardProps {
  iface: InterfaceInfo
  onEdit: (iface: InterfaceInfo) => void
  onPing: (target?: string) => void
}

export function InterfaceCard({ iface, onEdit, onPing }: InterfaceCardProps) {
  const primaryIp =
    iface.ipAddresses.length > 0 ? iface.ipAddresses[0] : '未分配 IP'
  const otherIps = iface.ipAddresses.slice(1)

  return (
    <Card
      data-testid={`interface-card-${iface.name}`}
      className={`relative transition-shadow hover:shadow-md ${
        iface.isCurrent ? 'border-primary/50 shadow-sm' : ''
      }`}
    >
      {iface.isCurrent && (
        <div className='absolute -top-3 right-4 rounded-full bg-primary px-2.5 py-0.5 text-xs font-medium text-primary-foreground shadow'>
          当前管理连接
        </div>
      )}

      <CardHeader className='pb-3'>
        <div className='flex items-center justify-between'>
          <div className='flex items-center gap-2.5'>
            <div
              className={`rounded-lg p-2 ${
                iface.linkUp
                  ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                  : 'bg-muted text-muted-foreground'
              }`}
            >
              <Network className='h-5 w-5' />
            </div>
            <div>
              <CardTitle className='flex items-center gap-2 text-base'>
                {iface.name}
                <Badge
                  variant={iface.linkUp ? 'default' : 'secondary'}
                  className={
                    iface.linkUp
                      ? 'bg-emerald-600 hover:bg-emerald-600 dark:bg-emerald-600'
                      : ''
                  }
                >
                  <span
                    className={`mr-1 inline-block h-1.5 w-1.5 rounded-full ${
                      iface.linkUp ? 'bg-white' : 'bg-muted-foreground'
                    }`}
                  />
                  {iface.linkUp ? '已连接 (Link Up)' : '未连接 (Carrier Down)'}
                </Badge>
              </CardTitle>
              <span className='font-mono text-xs text-muted-foreground'>
                MAC: {iface.mac || '00:00:00:00:00:00'}
              </span>
            </div>
          </div>

          <div className='flex items-center gap-1.5'>
            <Badge variant='outline' className='uppercase'>
              {iface.mode}
            </Badge>
            {iface.isDefaultGw && (
              <Badge
                variant='secondary'
                className='border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-800 dark:bg-blue-950 dark:text-blue-300'
              >
                默认网关
              </Badge>
            )}
          </div>
        </div>
      </CardHeader>

      <CardContent className='space-y-3 pb-4 text-sm'>
        {iface.isCurrent && (
          <div className='flex items-center gap-2 rounded-md bg-amber-500/10 p-2.5 text-xs text-amber-700 dark:text-amber-400'>
            <AlertTriangle className='h-4 w-4 shrink-0' />
            <span>
              您当前正通过此网卡访问管理面板，修改 IP
              可能导致会话迁移或临时断开。
            </span>
          </div>
        )}

        <div className='grid grid-cols-1 gap-2.5 rounded-lg bg-muted/40 p-3 sm:grid-cols-2'>
          <div>
            <div className='text-xs text-muted-foreground'>IPv4 地址</div>
            <div className='font-mono font-medium text-foreground'>
              {primaryIp}
            </div>
            {otherIps.length > 0 && (
              <div className='font-mono text-xs text-muted-foreground'>
                +{otherIps.join(', ')}
              </div>
            )}
          </div>

          <div>
            <div className='text-xs text-muted-foreground'>默认网关</div>
            <div className='font-mono font-medium text-foreground'>
              {iface.gateway || '—'}
            </div>
          </div>

          <div className='sm:col-span-2'>
            <div className='text-xs text-muted-foreground'>DNS 服务器</div>
            <div className='font-mono text-xs font-medium text-foreground'>
              {iface.dns && iface.dns.length > 0
                ? iface.dns.join(', ')
                : '未配置'}
            </div>
          </div>
        </div>
      </CardContent>

      <CardFooter className='flex items-center justify-between border-t pt-3'>
        <Button
          variant='ghost'
          size='sm'
          className='text-xs text-muted-foreground hover:text-foreground'
          onClick={() => onPing(iface.gateway || primaryIp.split('/')[0])}
        >
          <Activity className='mr-1.5 h-3.5 w-3.5' />
          探测连通性
        </Button>

        <Button
          variant='outline'
          size='sm'
          onClick={() => onEdit(iface)}
          className='gap-1.5'
        >
          <SlidersHorizontal className='h-3.5 w-3.5' />
          配置网卡
        </Button>
      </CardFooter>
    </Card>
  )
}
