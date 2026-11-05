import { useMemo } from 'react'
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusLevelClass, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  diffGb28181Config,
  type Gb28181Config,
  type Gb28181ConfigValue,
} from '../data/schema'

interface Gb28181RestartDiffProps {
  restartRequired: boolean
  desired: Gb28181Config
  startup: Gb28181Config
}

/**
 * 「已保存，需重启生效」横幅 + 运行值与待生效值的差异表。
 *
 * 差异只在 `restartRequired` 为真时才有意义，因此组件自己判空；
 * 调用方不需要再算一遍 `diffGb28181Config`。
 */
export function Gb28181RestartDiff({
  restartRequired,
  desired,
  startup,
}: Gb28181RestartDiffProps) {
  const { t } = useTranslation('gb28181')

  const differences = useMemo(
    () => diffGb28181Config(desired, startup),
    [desired, startup]
  )

  if (!restartRequired) return null

  const formatValue = (value: Gb28181ConfigValue): string => {
    if (typeof value === 'boolean') {
      return value ? t('value.on') : t('value.off')
    }
    if (typeof value === 'number') return String(value)
    return value.trim() === '' ? t('value.empty') : value
  }

  return (
    <Alert
      className={cn(
        'rounded-xl border shadow-2xs',
        statusLevelClass('warning')
      )}
    >
      <AlertTriangle className={cn('h-4 w-4', statusTextClass('warning'))} />
      <AlertTitle className='text-sm font-semibold tracking-tight'>
        {t('config.restartRequiredTitle')}
      </AlertTitle>
      <AlertDescription className='space-y-2.5 text-xs'>
        <p className='leading-relaxed'>{t('config.restartRequiredDesc')}</p>
        {differences.length > 0 && (
          <div className='overflow-hidden rounded-lg border border-border/60 bg-background/80 shadow-2xs'>
            <Table>
              <TableHeader>
                <TableRow className='hover:bg-transparent'>
                  <TableHead className='h-8 text-xs font-medium'>
                    {t('config.diffField')}
                  </TableHead>
                  <TableHead className='h-8 text-xs font-medium'>
                    {t('config.diffStartup')}
                  </TableHead>
                  <TableHead className='h-8 text-xs font-medium'>
                    {t('config.diffDesired')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {differences.map((difference) => (
                  <TableRow key={difference.field}>
                    <TableCell className='py-2 text-xs'>
                      {t(`fields.${difference.field}`)}
                    </TableCell>
                    <TableCell className='py-2 font-mono text-xs text-muted-foreground'>
                      {formatValue(difference.startup)}
                    </TableCell>
                    <TableCell className='py-2 font-mono text-xs font-semibold text-foreground'>
                      {formatValue(difference.desired)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </AlertDescription>
    </Alert>
  )
}
