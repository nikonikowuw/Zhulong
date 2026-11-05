import { useMemo, useState } from 'react'
import { useWatch, type Control, type UseFormSetValue } from 'react-hook-form'
import {
  Car,
  Check,
  ChevronDown,
  Filter,
  Globe,
  Layers,
  Search,
  ShieldAlert,
  UserCheck,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { AlertDestinationFormValues } from '../../data/schema'
import {
  useDestinationFilterOptions,
  type FilterOption,
} from '../../hooks/use-destination-filter-options'

interface DestinationFiltersSectionProps {
  control: Control<AlertDestinationFormValues>
  setValue: UseFormSetValue<AlertDestinationFormValues>
  initialFiltersOpen?: boolean
}

/**
 * 局部摘要：订阅选择数量判定是否定制
 */
function FiltersSummaryBadge({
  control,
}: {
  control: Control<AlertDestinationFormValues>
}) {
  const { t } = useTranslation('alertPush')
  const eventTypes = useWatch({ control, name: 'eventTypes' }) || []
  const algorithmIds = useWatch({ control, name: 'algorithmIds' }) || []
  const cameraIds = useWatch({ control, name: 'cameraIds' }) || []

  const isCustomized =
    eventTypes.length > 0 || algorithmIds.length > 0 || cameraIds.length > 0

  return (
    <span className='text-[11px] font-normal text-muted-foreground'>
      {isCustomized
        ? `(${t('advanced.filtersCustomSummary')})`
        : `(${t('advanced.filtersAllSummary')})`}
    </span>
  )
}

export function DestinationFiltersSection({
  control,
  setValue,
  initialFiltersOpen = false,
}: DestinationFiltersSectionProps) {
  const { t } = useTranslation('alertPush')
  const [filtersOpen, setFiltersOpen] = useState(initialFiltersOpen)
  const [algoSearch, setAlgoSearch] = useState('')
  const [camSearch, setCamSearch] = useState('')

  const { algorithms: availableAlgorithms, cameras: availableCameras } =
    useDestinationFilterOptions()

  const selectedEventTypes =
    useWatch({
      control,
      name: 'eventTypes',
    }) || []

  const selectedAlgoIds =
    useWatch({
      control,
      name: 'algorithmIds',
    }) || []

  const selectedCamIds =
    useWatch({
      control,
      name: 'cameraIds',
    }) || []

  // 过滤事件切换
  const toggleEventType = (et: string) => {
    const next = selectedEventTypes.includes(et)
      ? selectedEventTypes.filter((x) => x !== et)
      : [...selectedEventTypes, et]
    setValue('eventTypes', next)
  }

  // 算法选择
  const toggleAlgoSelection = (id: string) => {
    const next = selectedAlgoIds.includes(id)
      ? selectedAlgoIds.filter((x) => x !== id)
      : [...selectedAlgoIds, id]
    setValue('algorithmIds', next)
  }

  const handleSelectAllAlgos = () => {
    setValue(
      'algorithmIds',
      availableAlgorithms.map((a) => a.id)
    )
  }

  const handleClearAllAlgos = () => {
    setValue('algorithmIds', [])
  }

  // 摄像机选择
  const toggleCamSelection = (id: string) => {
    const next = selectedCamIds.includes(id)
      ? selectedCamIds.filter((x) => x !== id)
      : [...selectedCamIds, id]
    setValue('cameraIds', next)
  }

  const handleSelectAllCams = () => {
    setValue(
      'cameraIds',
      availableCameras.map((c) => c.id)
    )
  }

  const handleClearAllCams = () => {
    setValue('cameraIds', [])
  }

  const filteredAlgos = useMemo(() => {
    if (!algoSearch.trim()) return availableAlgorithms
    const q = algoSearch.toLowerCase()
    return availableAlgorithms.filter((a) => a.name.toLowerCase().includes(q))
  }, [availableAlgorithms, algoSearch])

  const filteredCams = useMemo(() => {
    if (!camSearch.trim()) return availableCameras
    const q = camSearch.toLowerCase()
    return availableCameras.filter((c) => c.name.toLowerCase().includes(q))
  }, [availableCameras, camSearch])

  return (
    <Collapsible
      open={filtersOpen}
      onOpenChange={setFiltersOpen}
      className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs transition-colors'
    >
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
          <Filter className='size-3.5 text-primary' />
          <span>{t('advanced.filtersTitle')}</span>
          <FiltersSummaryBadge control={control} />
        </div>

        <CollapsibleTrigger asChild>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-7 w-7 p-0'
          >
            <ChevronDown
              className={`size-4 transition-transform duration-200 ${
                filtersOpen ? 'rotate-180' : ''
              }`}
            />
            <span className='sr-only'>Toggle Filters</span>
          </Button>
        </CollapsibleTrigger>
      </div>

      <CollapsibleContent className='space-y-4 pt-1'>
        {/* 1. 事件类型订阅 */}
        <div className='space-y-2.5 rounded-lg border border-border/60 bg-background/60 p-3.5'>
          <div className='flex items-center justify-between border-b border-border/40 pb-2'>
            <Label className='text-xs font-semibold text-foreground'>
              {t('sections.filterEventTypes')}
            </Label>
            <span className='text-[11px] text-muted-foreground'>
              {selectedEventTypes.length === 0
                ? t('eventTypes.all')
                : t('filters.selectedCount', {
                    count: selectedEventTypes.length,
                  })}
            </span>
          </div>

          <div className='grid grid-cols-1 gap-2 sm:grid-cols-3'>
            {[
              {
                type: 'alarm',
                icon: ShieldAlert,
                label: t('eventTypes.alarm'),
                desc: t('eventTypes.alarmDesc'),
              },
              {
                type: 'face_recognition',
                icon: UserCheck,
                label: t('eventTypes.face_recognition'),
                desc: t('eventTypes.face_recognitionDesc'),
              },
              {
                type: 'plate_recognition',
                icon: Car,
                label: t('eventTypes.plate_recognition'),
                desc: t('eventTypes.plate_recognitionDesc'),
              },
            ].map(({ type, icon: Icon, label, desc }) => {
              const selected = selectedEventTypes.includes(type)
              return (
                <button
                  key={type}
                  type='button'
                  onClick={() => toggleEventType(type)}
                  className={cn(
                    'flex flex-col justify-between rounded-lg border p-2.5 text-start shadow-2xs transition-all',
                    selected
                      ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
                      : 'border-border/70 bg-background hover:border-border hover:bg-muted/30'
                  )}
                >
                  <div className='flex items-center justify-between'>
                    <div
                      className={cn(
                        'flex size-6 items-center justify-center rounded-md border text-xs',
                        selected
                          ? 'border-primary/40 bg-primary/10 text-primary'
                          : 'border-border/60 bg-muted/40 text-muted-foreground'
                      )}
                    >
                      <Icon className='size-3' />
                    </div>
                    {selected ? (
                      <div className='flex size-3.5 items-center justify-center rounded-full bg-primary text-primary-foreground'>
                        <Check className='size-2.5 stroke-[3]' />
                      </div>
                    ) : (
                      <div className='size-3.5 rounded-full border border-muted-foreground/30' />
                    )}
                  </div>
                  <div className='mt-2 space-y-0.5'>
                    <span className='text-xs font-semibold text-foreground'>
                      {label}
                    </span>
                    <p className='line-clamp-2 text-[10px] leading-relaxed text-muted-foreground'>
                      {desc}
                    </p>
                  </div>
                </button>
              )
            })}
          </div>
        </div>

        {/* 2. 算法过滤 */}
        <FilterOptionsSelector
          icon={Layers}
          title={t('sections.filterAlgos')}
          options={availableAlgorithms}
          filteredOptions={filteredAlgos}
          selectedIds={selectedAlgoIds}
          searchValue={algoSearch}
          onSearchChange={setAlgoSearch}
          onToggle={toggleAlgoSelection}
          onSelectAll={handleSelectAllAlgos}
          onClearAll={handleClearAllAlgos}
          searchPlaceholder={t('filters.searchAlgoPlaceholder')}
          emptyText={t('filters.noAlgos')}
          notFoundText={t('filters.noAlgosFound')}
          allActiveText={t('filters.allAlgosActive')}
          selectedCountText={(count) => t('filters.selectedCount', { count })}
          selectAllText={t('filters.selectAll')}
          clearAllText={t('filters.clearAll')}
        />

        {/* 3. 摄像机过滤 */}
        <FilterOptionsSelector
          icon={Globe}
          title={t('sections.filterCameras')}
          options={availableCameras}
          filteredOptions={filteredCams}
          selectedIds={selectedCamIds}
          searchValue={camSearch}
          onSearchChange={setCamSearch}
          onToggle={toggleCamSelection}
          onSelectAll={handleSelectAllCams}
          onClearAll={handleClearAllCams}
          searchPlaceholder={t('filters.searchCameraPlaceholder')}
          emptyText={t('filters.noCameras')}
          notFoundText={t('filters.noCamerasFound')}
          allActiveText={t('filters.allCamerasActive')}
          selectedCountText={(count) => t('filters.selectedCount', { count })}
          selectAllText={t('filters.selectAll')}
          clearAllText={t('filters.clearAll')}
        />
      </CollapsibleContent>
    </Collapsible>
  )
}

interface FilterOptionsSelectorProps {
  icon: typeof Layers
  title: string
  options: FilterOption[]
  filteredOptions: FilterOption[]
  selectedIds: string[]
  searchValue: string
  onSearchChange: (val: string) => void
  onToggle: (id: string) => void
  onSelectAll: () => void
  onClearAll: () => void
  searchPlaceholder: string
  emptyText: string
  notFoundText: string
  allActiveText: string
  selectedCountText: (count: number) => string
  selectAllText: string
  clearAllText: string
}

function FilterOptionsSelector({
  icon: Icon,
  title,
  options,
  filteredOptions,
  selectedIds,
  searchValue,
  onSearchChange,
  onToggle,
  onSelectAll,
  onClearAll,
  searchPlaceholder,
  emptyText,
  notFoundText,
  allActiveText,
  selectedCountText,
  selectAllText,
  clearAllText,
}: FilterOptionsSelectorProps) {
  return (
    <div className='space-y-2.5 rounded-lg border border-border/60 bg-background/60 p-3.5'>
      <div className='flex flex-wrap items-center justify-between gap-2 border-b border-border/40 pb-2'>
        <div className='flex items-center gap-2 text-xs font-semibold text-foreground'>
          <Icon className='size-3.5 text-primary' />
          <span>{title}</span>
          {selectedIds.length > 0 ? (
            <Badge variant='secondary' className='h-5 px-1.5 text-[10px]'>
              {selectedIds.length}
            </Badge>
          ) : null}
        </div>

        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={onSelectAll}
            disabled={options.length === 0}
            className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
          >
            {selectAllText}
          </Button>
          <span className='text-border'>|</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={onClearAll}
            disabled={selectedIds.length === 0}
            className='h-6 px-1.5 text-[11px] text-muted-foreground hover:text-foreground'
          >
            {clearAllText}
          </Button>
        </div>
      </div>

      {options.length > 6 ? (
        <div className='relative'>
          <Search className='absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground' />
          <Input
            placeholder={searchPlaceholder}
            value={searchValue}
            onChange={(e) => onSearchChange(e.target.value)}
            className='h-7.5 bg-background ps-8 text-xs'
          />
        </div>
      ) : null}

      <div className='flex max-h-28 min-h-14 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border/60 bg-muted/20 p-2'>
        {options.length === 0 ? (
          <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
            {emptyText}
          </div>
        ) : filteredOptions.length === 0 ? (
          <div className='flex h-full w-full items-center justify-center text-xs text-muted-foreground'>
            {notFoundText}
          </div>
        ) : (
          filteredOptions.map((opt) => {
            const selected = selectedIds.includes(opt.id)
            return (
              <Badge
                key={opt.id}
                variant={selected ? 'default' : 'outline'}
                className='cursor-pointer text-xs font-normal transition-colors'
                onClick={() => onToggle(opt.id)}
              >
                {opt.name}
              </Badge>
            )
          })
        )}
      </div>
      <p className='text-[11px] text-muted-foreground'>
        {selectedIds.length === 0
          ? allActiveText
          : selectedCountText(selectedIds.length)}
      </p>
    </div>
  )
}
