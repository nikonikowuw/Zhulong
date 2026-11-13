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

/**
 * 挂载点角色标记。这是「分类」而非「状态」，因此统一走中性兜底档，
 * 不占用 teal/amber/destructive/neutral 四个状态色相（§1.3.6 边界）。
 */
function mountTagKey(mountPoint: string): string | null {
  if (mountPoint === '/') return 'details.tagRoot'
  if (mountPoint === '/data') return 'details.tagData'
  if (mountPoint.startsWith('/mnt') || mountPoint.startsWith('/media')) {
    return 'details.tagExternal'
  }
  return null
}

export function StorageTable({ storage }: StorageTableProps) {
  const { t } = useTranslation('system')

  return (
    <Card className='gap-3 border-border/80 bg-card/75 p-5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/40 text-muted-foreground'>
            <HardDrive className='size-4' />
          </div>
          <div className='space-y-0.5'>
            <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
              {t('details.storageTitle', {
                defaultValue: '存储挂载分区详情',
              })}
            </CardTitle>
            <p className='text-xs text-muted-foreground'>
              {t('details.storageSubtitle', {
                defaultValue: '各文件系统卷的容量、余量与使用率',
              })}
            </p>
          </div>
        </div>
        {storage.length > 0 && (
          <Badge
            variant='outline'
            className='h-5 shrink-0 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
          >
            {t('details.storageSummary', {
              count: storage.length,
              defaultValue: `${storage.length} 个挂载卷`,
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
                  {t('details.storageColMount', { defaultValue: '挂载路径' })}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('details.storageColDevice', { defaultValue: '底层设备' })}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('details.storageColFSType', { defaultValue: '文件系统' })}
                </TableHead>
                <TableHead className='h-9 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.storageColTotal', { defaultValue: '总容量' })}
                </TableHead>
                <TableHead className='h-9 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.storageColUsed', { defaultValue: '已用容量' })}
                </TableHead>
                <TableHead className='h-9 text-end text-xs font-medium text-muted-foreground'>
                  {t('details.storageColFree', { defaultValue: '可用余量' })}
                </TableHead>
                <TableHead className='h-9 w-[240px] pe-3 text-xs font-medium text-muted-foreground'>
                  {t('details.storageColPercent', { defaultValue: '使用率' })}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {storage.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={7}
                    className='py-8 text-center text-xs text-muted-foreground'
                  >
                    {t('details.storageEmpty', {
                      defaultValue: '暂无挂载卷信息',
                    })}
                  </TableCell>
                </TableRow>
              ) : (
                storage.map((m) => {
                  const tagKey = mountTagKey(m.mountPoint)
                  return (
                    <TableRow
                      key={`${m.device}-${m.mountPoint}`}
                      className='border-border/50 transition-colors hover:bg-muted/30'
                    >
                      <TableCell className='py-2.5 ps-3'>
                        <div className='flex items-center gap-2'>
                          <span className='font-mono text-sm font-semibold text-foreground'>
                            {m.mountPoint}
                          </span>
                          {tagKey && (
                            <Badge
                              variant='outline'
                              className='h-4 shrink-0 border-neutral-300 bg-neutral-300/40 px-1 py-0 font-mono text-[9px] font-medium text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
                            >
                              {t(tagKey)}
                            </Badge>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className='py-2.5 font-mono text-xs text-muted-foreground'>
                        {m.device}
                      </TableCell>
                      <TableCell className='py-2.5'>
                        <Badge
                          variant='secondary'
                          className='h-5 px-1.5 py-0 font-mono text-[10px] font-medium uppercase'
                        >
                          {m.fsType}
                        </Badge>
                      </TableCell>
                      <TableCell className='py-2.5 text-end font-mono text-xs text-muted-foreground tabular-nums'>
                        {formatBytes(m.totalBytes)}
                      </TableCell>
                      <TableCell className='py-2.5 text-end font-mono text-sm font-medium text-foreground tabular-nums'>
                        {formatBytes(m.usedBytes)}
                      </TableCell>
                      <TableCell className='py-2.5 text-end font-mono text-xs text-muted-foreground tabular-nums'>
                        {formatBytes(m.freeBytes)}
                      </TableCell>
                      <TableCell className='py-2.5 pe-3'>
                        <div className='flex items-center gap-3'>
                          <div
                            role='progressbar'
                            aria-valuenow={m.usedPercent}
                            aria-valuemin={0}
                            aria-valuemax={100}
                            aria-label={m.mountPoint}
                            className='h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-muted/80'
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
                            className={`w-14 shrink-0 text-end font-mono text-xs font-semibold tabular-nums ${getUsageStatusColor(
                              m.usedPercent
                            )}`}
                          >
                            {m.usedPercent.toFixed(1)}%
                          </span>
                        </div>
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
