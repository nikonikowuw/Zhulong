import { useTranslation } from 'react-i18next'
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { AlertDestination } from '../types'
import { AlertDestinationTableRow } from './alert-destination-table-row'

interface AlertDestinationsTableProps {
  destinations: AlertDestination[]
  onEdit: (dest: AlertDestination) => void
  onDelete: (dest: AlertDestination) => void
  onToggle: (dest: AlertDestination, enabled: boolean) => void
  onTest: (dest: AlertDestination) => void
}

export function AlertDestinationsTable({
  destinations,
  onEdit,
  onDelete,
  onToggle,
  onTest,
}: AlertDestinationsTableProps) {
  const { t } = useTranslation('alertPush')

  return (
    <div className='rounded-md border bg-card'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-[200px]'>{t('fields.name')}</TableHead>
            <TableHead className='w-[140px]'>
              {t('fields.channelType')}
            </TableHead>
            <TableHead className='w-[100px]'>{t('fields.imageMode')}</TableHead>
            <TableHead className='w-[90px]'>{t('fields.enabled')}</TableHead>
            <TableHead className='w-[140px]'>{t('fields.status')}</TableHead>
            <TableHead className='w-[120px]'>{t('fields.queue')}</TableHead>
            <TableHead className='w-[160px]'>
              {t('fields.lastAttempt')}
            </TableHead>
            <TableHead className='w-[80px] text-end'>
              {t('fields.actions')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {destinations.map((dest) => (
            <AlertDestinationTableRow
              key={dest.id}
              dest={dest}
              onEdit={onEdit}
              onDelete={onDelete}
              onToggle={onToggle}
              onTest={onTest}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
