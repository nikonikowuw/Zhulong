import { Network } from 'lucide-react'
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

export function NetworkTable({ network }: NetworkTableProps) {
  const { t } = useTranslation('system')

  return (
    <Card className='gap-3 p-4'>
      <CardHeader className='flex-row items-center justify-between p-0'>
        <CardTitle className='flex items-center gap-2 text-base font-semibold'>
          <Network className='size-4 text-primary' />
          <span>
            {t('details.networkTitle', {
              defaultValue: 'Network Interfaces',
            })}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className='p-0 pt-2'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>
                {t('details.networkColIface', { defaultValue: 'Interface' })}
              </TableHead>
              <TableHead>
                {t('details.networkColStatus', { defaultValue: 'State' })}
              </TableHead>
              <TableHead>
                {t('details.networkColRxRate', { defaultValue: 'Rx Rate' })}
              </TableHead>
              <TableHead>
                {t('details.networkColTxRate', { defaultValue: 'Tx Rate' })}
              </TableHead>
              <TableHead>
                {t('details.networkColRxTotal', { defaultValue: 'Total Rx' })}
              </TableHead>
              <TableHead>
                {t('details.networkColTxTotal', { defaultValue: 'Total Tx' })}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {network.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className='py-6 text-center text-sm text-muted-foreground'
                >
                  -
                </TableCell>
              </TableRow>
            ) : (
              network.map((net) => (
                <TableRow key={net.interface}>
                  <TableCell className='font-mono font-medium'>
                    {net.interface}
                  </TableCell>
                  <TableCell>
                    {net.isUp ? (
                      <Badge
                        variant='outline'
                        className='gap-1.5 border-emerald-500/30 text-xs font-normal text-emerald-600 dark:text-emerald-400'
                      >
                        <span className='size-1.5 rounded-full bg-emerald-500' />
                        {t('details.up', { defaultValue: 'UP' })}
                      </Badge>
                    ) : (
                      <Badge
                        variant='outline'
                        className='gap-1.5 border-muted-foreground/30 text-xs font-normal text-muted-foreground'
                      >
                        <span className='size-1.5 rounded-full bg-muted-foreground' />
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
      </CardContent>
    </Card>
  )
}
