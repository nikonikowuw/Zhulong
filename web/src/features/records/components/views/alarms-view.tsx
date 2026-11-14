import { useCallback, useMemo } from 'react'
import { Bell } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type CameraNameLookup } from '../../data/camera-label'
import { type Alarm } from '../../data/schema'
import { AlarmCard } from '../alarms/alarm-card'
import { buildAlarmColumns } from './alarms-columns'
import { RecordsListShell } from './records-list-shell'

export interface AlarmsViewProps {
  items: Alarm[]
  total: number
  page: number
  pageSize: number
  view: 'grid' | 'table'
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
  isLoading: boolean
  isError: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSelect: (item: Alarm) => void
  onRetry: () => void
  /** 多选支持 */
  isSelected?: (id: string) => boolean
  onToggleSelect?: (item: Alarm) => void
  isAllPageSelected?: boolean
  isPartiallySelected?: boolean
  onToggleSelectAll?: () => void
}

export function AlarmsView({
  items,
  total,
  page,
  pageSize,
  view,
  cameraName,
  isLoading,
  isError,
  onPageChange,
  onPageSizeChange,
  onSelect,
  onRetry,
  isSelected,
  onToggleSelect,
  isAllPageSelected,
  isPartiallySelected,
  onToggleSelectAll,
}: AlarmsViewProps) {
  const { t } = useTranslation('records')

  const columns = useMemo(
    () => buildAlarmColumns(t, cameraName),
    [t, cameraName]
  )

  const renderCard = useCallback(
    (item: Alarm) => (
      <AlarmCard item={item} cameraName={cameraName} onSelect={onSelect} />
    ),
    [cameraName, onSelect]
  )

  return (
    <RecordsListShell
      items={items}
      total={total}
      page={page}
      pageSize={pageSize}
      view={view}
      columns={columns}
      isLoading={isLoading}
      isError={isError}
      onPageChange={onPageChange}
      onPageSizeChange={onPageSizeChange}
      onSelect={onSelect}
      onRetry={onRetry}
      isSelected={isSelected}
      onToggleSelect={onToggleSelect}
      isAllPageSelected={isAllPageSelected}
      isPartiallySelected={isPartiallySelected}
      onToggleSelectAll={onToggleSelectAll}
      renderCard={renderCard}
      emptyIcon={<Bell className='size-7 text-muted-foreground' />}
      emptyTitle={t('table.empty')}
      emptyDesc={t('table.emptyDesc')}
      skeletonKeyPrefix='alarm'
    />
  )
}
