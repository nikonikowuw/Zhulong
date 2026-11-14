import { useCallback, useMemo } from 'react'
import { Camera } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type CameraNameLookup } from '../../data/camera-label'
import { type Capture } from '../../data/schema'
import { CaptureCard } from '../captures/capture-card'
import { VehicleCaptureCard } from '../vehicles/vehicle-capture-card'
import { buildCaptureColumns } from './captures-columns'
import { RecordsListShell } from './records-list-shell'

export interface CapturesViewProps {
  items: Capture[]
  total: number
  page: number
  pageSize: number
  view: 'grid' | 'table'
  mode?: 'captures' | 'vehicles'
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
  isLoading: boolean
  isError: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSelect: (item: Capture) => void
  onRetry: () => void
  /** 多选支持 */
  isSelected?: (id: string) => boolean
  onToggleSelect?: (item: Capture) => void
  isAllPageSelected?: boolean
  isPartiallySelected?: boolean
  onToggleSelectAll?: () => void
}

export function CapturesView({
  items,
  total,
  page,
  pageSize,
  view,
  mode = 'captures',
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
}: CapturesViewProps) {
  const { t } = useTranslation('records')

  const columns = useMemo(
    () => buildCaptureColumns(mode, t, cameraName),
    [mode, t, cameraName]
  )

  const renderCard = useCallback(
    (item: Capture) =>
      mode === 'vehicles' ? (
        <VehicleCaptureCard
          item={item}
          cameraName={cameraName}
          onSelect={onSelect}
        />
      ) : (
        <CaptureCard item={item} cameraName={cameraName} onSelect={onSelect} />
      ),
    [mode, cameraName, onSelect]
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
      emptyIcon={<Camera className='size-7 text-muted-foreground' />}
      emptyTitle={t(
        mode === 'vehicles' ? 'table.emptyVehicles' : 'table.empty'
      )}
      emptyDesc={t(
        mode === 'vehicles' ? 'table.emptyVehiclesDesc' : 'table.emptyDesc'
      )}
      skeletonKeyPrefix='cap'
      gridSkeletonAspectClass={
        mode === 'vehicles' ? 'aspect-[3/2]' : 'aspect-square'
      }
      tableThumbnailSkeletonClass={
        mode === 'vehicles' ? 'h-14 w-40' : 'size-10'
      }
    />
  )
}
