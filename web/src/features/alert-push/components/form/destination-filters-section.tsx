import { useMemo, useState } from 'react'
import { useWatch, type Control, type UseFormSetValue } from 'react-hook-form'
import {
  BellRing,
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
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type { AlertDestinationFormValues } from '../../data/schema'
import {
  useDestinationFilterOptions,
  type FilterOption,
} from '../../hooks/use-destination-filter-options'
import type { ImageMode } from '../../types'

const IMAGE_MODE_DESC_KEYS: Record<string, string> = {
  url: 'imageModes.urlDesc',
  multipart: 'imageModes.multipartDesc',
  base64: 'imageModes.base64Desc',
}

const EVENT_TYPE_OPTIONS = [
  {
    type: 'alarm',
    icon: ShieldAlert,
    labelKey: 'eventTypes.alarm',
    descKey: 'eventTypes.alarmDesc',
  },
  {
    type: 'face_recognition',
    icon: UserCheck,
    labelKey: 'eventTypes.face_recognition',
    descKey: 'eventTypes.face_recognitionDesc',
  },
  {
    type: 'plate_recognition',
    icon: Car,
    labelKey: 'eventTypes.plate_recognition',
    descKey: 'eventTypes.plate_recognitionDesc',
  },
] as const

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

  const eventsEnabled = useWatch({
    control,
    name: 'eventsEnabled',
  })

  const channelType = useWatch({
    control,
    name: 'channelType',
  })

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
    <div className='space-y-3.5 rounded-xl border border-border/80 bg-card/60 p-4 shadow-2xs transition-colors'>
      {/* 能力头：感知事件推送开关 */}
      <FormField
        control={control}
        name='eventsEnabled'
        render={({ field }) => (
          <FormItem className='space-y-1.5'>
            <div className='flex items-start justify-between gap-3'>
              <div className='flex items-start gap-2.5'>
                <div className='mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-background text-primary shadow-2xs'>
                  <BellRing className='size-3.5' />
                </div>
                <div className='space-y-0.5'>
                  <FormLabel className='cursor-pointer text-xs font-semibold text-foreground'>
                    {t('capabilities.eventsTitle')}
                  </FormLabel>
                  <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
                    {t('capabilities.eventsDesc')}
                  </FormDescription>
                </div>
              </div>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  aria-label={t('capabilities.eventsTitle')}
                />
              </FormControl>
            </div>
            <FormMessage className='text-xs' />
          </FormItem>
        )}
      />

      {eventsEnabled ? (
        <div className='space-y-3.5 border-t border-border/50 pt-3.5'>
          {/* 图片交付模式 */}
          <FormField
            control={control}
            name='imageMode'
            render={({ field }) => {
              const currentMode =
                field.value || (channelType === 'mqtt' ? 'base64' : 'multipart')
              const descKey = currentMode
                ? IMAGE_MODE_DESC_KEYS[currentMode]
                : undefined

              return (
                <FormItem className='space-y-1.5 rounded-lg border border-border/60 bg-muted/20 p-3'>
                  <FormLabel className='text-xs font-medium text-foreground'>
                    {t('fields.imageMode')}
                  </FormLabel>

                  <FormControl>
                    <Select
                      value={currentMode}
                      onValueChange={(val) => field.onChange(val as ImageMode)}
                    >
                      <SelectTrigger className='h-8 w-full bg-background text-xs font-medium'>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value='url'>
                          <div className='flex flex-col py-0.5 text-start'>
                            <span className='text-xs font-medium text-foreground'>
                              {t('imageModes.url')}
                            </span>
                            <span className='text-[11px] text-muted-foreground'>
                              {t('imageModes.urlDesc')}
                            </span>
                          </div>
                        </SelectItem>
                        {channelType === 'webhook' ? (
                          <SelectItem value='multipart'>
                            <div className='flex flex-col py-0.5 text-start'>
                              <span className='text-xs font-medium text-foreground'>
                                {t('imageModes.multipart')}
                              </span>
                              <span className='text-[11px] text-muted-foreground'>
                                {t('imageModes.multipartDesc')}
                              </span>
                            </div>
                          </SelectItem>
                        ) : null}
                        {channelType === 'mqtt' ? (
                          <SelectItem value='base64'>
                            <div className='flex flex-col py-0.5 text-start'>
                              <span className='text-xs font-medium text-foreground'>
                                {t('imageModes.base64')}
                              </span>
                              <span className='text-[11px] text-muted-foreground'>
                                {t('imageModes.base64Desc')}
                              </span>
                            </div>
                          </SelectItem>
                        ) : null}
                      </SelectContent>
                    </Select>
                  </FormControl>

                  {descKey ? (
                    <FormDescription className='text-[11px] leading-relaxed text-muted-foreground'>
                      {t(descKey)}
                    </FormDescription>
                  ) : null}
                  <FormMessage className='text-xs' />
                </FormItem>
              )
            }}
          />

          {/* 可折叠的事件与通道过滤 */}
          <Collapsible
            open={filtersOpen}
            onOpenChange={setFiltersOpen}
            className='space-y-3 rounded-lg border border-border/60 bg-muted/20 p-3 transition-colors'
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
                  <span className='sr-only'>{t('actions.toggleFilters')}</span>
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
                  {EVENT_TYPE_OPTIONS.map(
                    ({ type, icon: Icon, labelKey, descKey }) => {
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
                              {t(labelKey)}
                            </span>
                            <p className='line-clamp-2 text-[10px] leading-relaxed text-muted-foreground'>
                              {t(descKey)}
                            </p>
                          </div>
                        </button>
                      )
                    }
                  )}
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
                selectedCountText={(count) =>
                  t('filters.selectedCount', { count })
                }
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
                selectedCountText={(count) =>
                  t('filters.selectedCount', { count })
                }
                selectAllText={t('filters.selectAll')}
                clearAllText={t('filters.clearAll')}
              />
            </CollapsibleContent>
          </Collapsible>
        </div>
      ) : null}
    </div>
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
  const selectedIdSet = useMemo(() => new Set(selectedIds), [selectedIds])

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
          <Search className='absolute start-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground' />
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
            const selected = selectedIdSet.has(opt.id)
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
