import { HardDrive } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type StorageMountStatus } from '../types'
import {
  formatBytes,
  getProgressIndicatorClass,
  getUsageStatusColor,
} from '../utils/format'

interface StorageTableProps {
  storage: StorageMountStatus[]
}

export function StorageTable({ storage }: StorageTableProps) {
  const { t } = useTranslation('system')

  return (
    <Card className='gap-3 p-4'>
      <CardHeader className='flex-row items-center justify-between p-0'>
        <CardTitle className='flex items-center gap-2 text-base font-semibold'>
          <HardDrive className='size-4 text-primary' />
          <span>
            {t('details.storageTitle', {
              defaultValue: 'Filesystem Mounts',
            })}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className='p-0 pt-2'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>
                {t('details.storageColMount', { defaultValue: 'Mount' })}
              </TableHead>
              <TableHead>
                {t('details.storageColDevice', { defaultValue: 'Device' })}
              </TableHead>
              <TableHead>
                {t('details.storageColFSType', { defaultValue: 'FS' })}
              </TableHead>
              <TableHead>
                {t('details.storageColTotal', { defaultValue: 'Total' })}
              </TableHead>
              <TableHead>
                {t('details.storageColUsed', { defaultValue: 'Used' })}
              </TableHead>
              <TableHead>
                {t('details.storageColFree', { defaultValue: 'Free' })}
              </TableHead>
              <TableHead className='w-[160px]'>
                {t('details.storageColPercent', { defaultValue: 'Usage' })}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {storage.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className='py-6 text-center text-sm text-muted-foreground'
                >
                  -
                </TableCell>
              </TableRow>
            ) : (
              storage.map((m) => (
                <TableRow key={`${m.device}-${m.mountPoint}`}>
                  <TableCell className='font-mono font-medium'>
                    {m.mountPoint}
                  </TableCell>
                  <TableCell className='font-mono text-xs text-muted-foreground'>
                    {m.device}
                  </TableCell>
                  <TableCell className='text-xs font-medium uppercase'>
                    {m.fsType}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>
                    {formatBytes(m.totalBytes)}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>
                    {formatBytes(m.usedBytes)}
                  </TableCell>
                  <TableCell className='font-mono text-xs text-muted-foreground'>
                    {formatBytes(m.freeBytes)}
                  </TableCell>
                  <TableCell>
                    <div className='flex items-center gap-2'>
                      <div
                        role='progressbar'
                        aria-valuenow={m.usedPercent}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-label={m.mountPoint}
                        className='h-2 w-24 overflow-hidden rounded-full bg-muted'
                      >
                        <div
                          className={`h-full transition-all duration-500 ${getProgressIndicatorClass(
                            m.usedPercent
                          )}`}
                          style={{
                            width: `${Math.min(
                              100,
                              Math.max(0, m.usedPercent)
                            )}%`,
                          }}
                        />
                      </div>
                      <span
                        className={`font-mono text-xs font-medium ${getUsageStatusColor(
                          m.usedPercent
                        )}`}
                      >
                        {m.usedPercent.toFixed(1)}%
                      </span>
                    </div>
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
