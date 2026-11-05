import { Globe, Network, Radio, Repeat } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type NetworkInterfaceStatus } from '../types'
import { formatBytes, formatRate } from '../utils/format'

interface NetworkTableProps {
  network: NetworkInterfaceStatus[]
}

function getInterfaceIcon(iface: string) {
  if (iface.startsWith('eth') || iface.startsWith('en')) {
    return <Network className='size-3 text-emerald-500' />
  }
  if (iface.startsWith('wl')) {
    return <Radio className='size-3 text-sky-500' />
  }
  if (iface === 'lo') {
    return <Repeat className='size-3 text-muted-foreground' />
  }
  return <Globe className='size-3 text-purple-500' />
}

export function NetworkTable({ network }: NetworkTableProps) {
  const { t } = useTranslation('system')

  const upCount = network.filter((n) => n.isUp).length

  return (
    <Card className='gap-3 border-border/80 bg-card/75 p-4.5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-7 shrink-0 items-center justify-center rounded-lg border border-emerald-500/20 bg-emerald-500/10 text-emerald-500 shadow-2xs'>
            <Network className='size-3.5' />
          </div>
          <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
            {t('details.networkTitle', {
              defaultValue: '网络接口流量与状态',
            })}
          </CardTitle>
        </div>
        {network.length > 0 && (
          <Badge
            variant='outline'
            className='h-5 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
          >
            {upCount} 在线 / {network.length} 接口
          </Badge>
        )}
      </CardHeader>
      <CardContent className='p-0 pt-3'>
        <div className='overflow-x-auto rounded-lg border border-border/60'>
          <Table>
            <TableHeader className='bg-muted/40'>
              <TableRow className='hover:bg-transparent'>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColIface', { defaultValue: '网络接口' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColStatus', { defaultValue: '连接状态' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColRxRate', { defaultValue: '当前下行速率' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColTxRate', { defaultValue: '当前上行速率' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColRxTotal', { defaultValue: '累计接收流量' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.networkColTxTotal', { defaultValue: '累计发送流量' })}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {network.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='py-6 text-center text-xs text-muted-foreground'
                  >
                    暂无网络接口
                  </TableCell>
                </TableRow>
              ) : (
                network.map((net) => (
                  <TableRow
                    key={net.interface}
                    className='transition-colors hover:bg-muted/30'
                  >
                    <TableCell className='font-mono text-xs font-semibold'>
                      <div className='flex items-center gap-1.5'>
                        {getInterfaceIcon(net.interface)}
                        <span>{net.interface}</span>
                      </div>
                    </TableCell>
                    <TableCell>
                      {net.isUp ? (
                        <Badge
                          variant='outline'
                          className='h-5 gap-1 border-emerald-500/30 bg-emerald-500/10 px-1.5 text-[11px] font-normal text-emerald-600 dark:text-emerald-400'
                        >
                          <span className='size-1.5 rounded-full bg-emerald-500 shadow-2xs' />
                          {t('details.up', { defaultValue: 'UP' })}
                        </Badge>
                      ) : (
                        <Badge
                          variant='outline'
                          className='h-5 gap-1 border-muted-foreground/30 bg-muted/40 px-1.5 text-[11px] font-normal text-muted-foreground'
                        >
                          <span className='size-1.5 rounded-full bg-muted-foreground/60' />
                          {t('details.down', { defaultValue: 'DOWN' })}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className='font-mono text-xs font-medium text-primary'>
                      {formatRate(net.rxBytesRate)}
                    </TableCell>
                    <TableCell className='font-mono text-xs font-medium text-emerald-600 dark:text-emerald-400'>
                      {formatRate(net.txBytesRate)}
                    </TableCell>
                    <TableCell className='font-mono text-xs text-muted-foreground'>
                      {formatBytes(net.rxTotalBytes)}
                    </TableCell>
                    <TableCell className='font-mono text-xs text-muted-foreground'>
                      {formatBytes(net.txTotalBytes)}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  )
}
