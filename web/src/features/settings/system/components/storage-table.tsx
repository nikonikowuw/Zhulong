import { HardDrive } from 'lucide-react'
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
    <Card className='gap-3 border-border/80 bg-card/75 p-4.5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-7 shrink-0 items-center justify-center rounded-lg border border-cyan-500/20 bg-cyan-500/10 text-cyan-500 shadow-2xs'>
            <HardDrive className='size-3.5' />
          </div>
          <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
            {t('details.storageTitle', {
              defaultValue: '存储挂载分区详情',
            })}
          </CardTitle>
        </div>
        {storage.length > 0 && (
          <Badge
            variant='outline'
            className='h-5 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
          >
            {storage.length} 挂载卷
          </Badge>
        )}
      </CardHeader>
      <CardContent className='p-0 pt-3'>
        <div className='overflow-x-auto rounded-lg border border-border/60'>
          <Table>
            <TableHeader className='bg-muted/40'>
              <TableRow className='hover:bg-transparent'>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColMount', { defaultValue: '挂载路径' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColDevice', { defaultValue: '底层设备' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColFSType', { defaultValue: '文件系统' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColTotal', { defaultValue: '总容量' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColUsed', { defaultValue: '已用容量' })}
                </TableHead>
                <TableHead className='h-8 text-xs font-medium'>
                  {t('details.storageColFree', { defaultValue: '可用余量' })}
                </TableHead>
                <TableHead className='h-8 w-[150px] text-xs font-medium'>
                  {t('details.storageColPercent', { defaultValue: '使用率' })}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {storage.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={7}
                    className='py-6 text-center text-xs text-muted-foreground'
                  >
                    暂无挂载卷信息
                  </TableCell>
                </TableRow>
              ) : (
                storage.map((m) => (
                  <TableRow
                    key={`${m.device}-${m.mountPoint}`}
                    className='transition-colors hover:bg-muted/30'
                  >
                    <TableCell className='font-mono text-xs font-semibold'>
                      <div className='flex items-center gap-1.5'>
                        <span>{m.mountPoint}</span>
                        {m.mountPoint === '/' && (
                          <Badge
                            variant='outline'
                            className='h-4 border-primary/30 bg-primary/10 px-1 py-0 text-[9px] font-normal text-primary'
                          >
                            ROOT
                          </Badge>
                        )}
                        {m.mountPoint === '/data' && (
                          <Badge
                            variant='outline'
                            className='h-4 border-amber-500/30 bg-amber-500/10 px-1 py-0 text-[9px] font-normal text-amber-600 dark:text-amber-400'
                          >
                            DATA
                          </Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className='font-mono text-xs text-muted-foreground'>
                      {m.device}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant='secondary'
                        className='h-4 px-1 py-0 font-mono text-[10px] font-medium uppercase'
                      >
                        {m.fsType}
                      </Badge>
                    </TableCell>
                    <TableCell className='font-mono text-xs font-medium'>
                      {formatBytes(m.totalBytes)}
                    </TableCell>
                    <TableCell className='font-mono text-xs font-medium text-foreground'>
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
                          className='h-1.5 w-20 overflow-hidden rounded-full bg-muted/80'
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
                          className={`font-mono text-xs font-semibold ${getUsageStatusColor(
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
        </div>
      </CardContent>
    </Card>
  )
}
