import { useCallback, useMemo } from 'react'
import { UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type CameraNameLookup } from '../../data/camera-label'
import { type Recognition } from '../../data/schema'
import { RecognitionCard } from '../recognitions/recognition-card'
import { buildRecognitionColumns } from './recognitions-columns'
import { RecordsListShell } from './records-list-shell'

export interface RecognitionsViewProps {
  items: Recognition[]
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
  onSelect: (item: Recognition) => void
  onRetry: () => void
  /** 多选支持 */
  isSelected?: (id: string) => boolean
  onToggleSelect?: (item: Recognition) => void
  isAllPageSelected?: boolean
  isPartiallySelected?: boolean
  onToggleSelectAll?: () => void
}

export function RecognitionsView({
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
}: RecognitionsViewProps) {
  const { t } = useTranslation('records')

  const columns = useMemo(
    () => buildRecognitionColumns(t, cameraName),
    [t, cameraName]
  )

  const renderCard = useCallback(
    (item: Recognition) => (
      <RecognitionCard
        item={item}
        cameraName={cameraName}
        onSelect={onSelect}
      />
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
      emptyIcon={<UserCheck className='size-7 text-muted-foreground' />}
      emptyTitle={t('table.empty')}
      emptyDesc={t('table.emptyDesc')}
      skeletonKeyPrefix='rec'
    />
  )
}
