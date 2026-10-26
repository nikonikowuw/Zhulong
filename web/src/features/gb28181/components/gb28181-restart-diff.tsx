import { useMemo } from 'react'
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
    <Alert className='border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200'>
      <AlertTriangle className='h-4 w-4' />
      <AlertTitle className='text-sm font-semibold'>
        {t('config.restartRequiredTitle')}
      </AlertTitle>
      <AlertDescription className='space-y-2 text-xs'>
        <p>{t('config.restartRequiredDesc')}</p>
        {differences.length > 0 && (
          <div className='overflow-hidden rounded-md border border-amber-500/30 bg-background/60'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className='h-8 text-[11px]'>
                    {t('config.diffField')}
                  </TableHead>
                  <TableHead className='h-8 text-[11px]'>
                    {t('config.diffStartup')}
                  </TableHead>
                  <TableHead className='h-8 text-[11px]'>
                    {t('config.diffDesired')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {differences.map((difference) => (
                  <TableRow key={difference.field}>
                    <TableCell className='py-1.5 text-xs'>
                      {t(`fields.${difference.field}`)}
                    </TableCell>
                    <TableCell className='py-1.5 font-mono text-xs'>
                      {formatValue(difference.startup)}
                    </TableCell>
                    <TableCell className='py-1.5 font-mono text-xs font-medium'>
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
