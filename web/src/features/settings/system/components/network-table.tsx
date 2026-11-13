import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Globe,
  Network,
  Radio,
  Repeat,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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

/**
 * 按网卡命名前缀挑图标形状，颜色统一取 `text-muted-foreground`。
 * 形状已经足以区分有线/无线/回环/虚拟，再给每种类型配一个色相只会把
 * 「状态色」的语义稀释掉。
 */
function getInterfaceIcon(iface: string) {
  if (iface.startsWith('eth') || iface.startsWith('en')) {
    return <Network className='size-3.5 text-muted-foreground' />
  }
  if (iface.startsWith('wl')) {
    return <Radio className='size-3.5 text-muted-foreground' />
  }
  if (iface === 'lo') {
    return <Repeat className='size-3.5 text-muted-foreground/70' />
  }
  return <Globe className='size-3.5 text-muted-foreground' />
}

export function NetworkTable({ network }: NetworkTableProps) {
  const { t } = useTranslation('system')

  const upCount = network.filter((n) => n.isUp).length

  return (
    <Card className='gap-3 border-border/80 bg-card/75 p-5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/40 text-muted-foreground'>
            <Network className='size-4' />
          </div>
          <div className='space-y-0.5'>
            <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
              {t('details.networkTitle', {
                defaultValue: '网络接口流量与状态',
              })}
            </CardTitle>
            <p className='text-xs text-muted-foreground'>
              {t('details.networkSubtitle', {
                defaultValue: '各接口链路状态、即时速率与累计流量',
              })}
            </p>
          </div>
        </div>
        {network.length > 0 && (
          <Badge
            variant='outline'
            className='h-5 shrink-0 gap-1.5 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
          >
            <span
              className={cn('size-1.5 rounded-full', STATUS_DOT_CLASS.active)}
            />
            {t('details.networkSummary', {
              upCount,
              total: network.length,
              defaultValue: `${upCount} 在线 / ${network.length} 接口`,
            })}
          </Badge>
        )}
      </CardHeader>

      <CardContent className='p-0 pt-3'>
        <div className='overflow-x-auto rounded-lg border border-border/60'>
          <Table>
            <TableHeader>
              <TableRow className='border-border/60 bg-muted/40 hover:bg-muted/40'>
                <TableHead className='h-9 ps-3 text-xs font-medium text-muted-foreground'>
                  {t('details.networkColIface', { defaultValue: '网络接口' })}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('details.networkColStatus', { defaultValue: '连接状态' })}
                </TableHead>
                <TableHead className='h-9 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.networkColRxRate', {
                    defaultValue: '当前下行速率',
                  })}
                </TableHead>
                <TableHead className='h-9 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.networkColTxRate', {
                    defaultValue: '当前上行速率',
                  })}
                </TableHead>
                <TableHead className='h-9 pe-3 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.networkColRxTotal', {
                    defaultValue: '累计接收流量',
                  })}
                </TableHead>
                <TableHead className='h-9 pe-3 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.networkColTxTotal', {
                    defaultValue: '累计发送流量',
                  })}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {network.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='py-8 text-center text-xs text-muted-foreground'
                  >
                    {t('details.networkEmpty', {
                      defaultValue: '暂无网络接口',
                    })}
                  </TableCell>
                </TableRow>
              ) : (
                network.map((net) => {
                  const level = net.isUp ? 'active' : 'inactive'
                  return (
                    <TableRow
                      key={net.interface}
                      className='border-border/50 transition-colors hover:bg-muted/30'
                    >
                      <TableCell className='py-2.5 ps-3'>
                        <div className='flex items-center gap-2'>
                          {getInterfaceIcon(net.interface)}
                          <span className='font-mono text-sm font-semibold text-foreground'>
                            {net.interface}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className='py-2.5'>
                        <Badge
                          variant='outline'
                          className={cn(
                            'h-5 gap-1.5 px-1.5 text-[11px] font-normal',
                            STATUS_LEVEL_CLASS[level]
                          )}
                        >
                          <span
                            className={cn(
                              'size-1.5 rounded-full',
                              STATUS_DOT_CLASS[level]
                            )}
                          />
                          {net.isUp
                            ? t('details.up', { defaultValue: 'UP' })
                            : t('details.down', { defaultValue: 'DOWN' })}
                        </Badge>
                      </TableCell>
                      <TableCell className='py-2.5 text-end'>
                        <span className='inline-flex items-center gap-1 font-mono text-sm font-medium text-foreground tabular-nums'>
                          <ArrowDownToLine className='size-3 shrink-0 text-muted-foreground' />
                          {formatRate(net.rxBytesRate)}
                        </span>
                      </TableCell>
                      <TableCell className='py-2.5 text-end'>
                        <span className='inline-flex items-center gap-1 font-mono text-sm font-medium text-foreground tabular-nums'>
                          <ArrowUpFromLine className='size-3 shrink-0 text-muted-foreground' />
                          {formatRate(net.txBytesRate)}
                        </span>
                      </TableCell>
                      <TableCell className='py-2.5 pe-3 text-end font-mono text-xs text-muted-foreground tabular-nums'>
                        {formatBytes(net.rxTotalBytes)}
                      </TableCell>
                      <TableCell className='py-2.5 pe-3 text-end font-mono text-xs text-muted-foreground tabular-nums'>
                        {formatBytes(net.txTotalBytes)}
                      </TableCell>
                    </TableRow>
                  )
                })
              )}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  )
}
