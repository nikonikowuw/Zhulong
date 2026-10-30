import { useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { CarFront, Loader2, RotateCcw, Search, User } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAllPersons } from '@/features/persons/contract'
import {
  createTrajectorySearchFormSchema,
  type TimeRangePreset,
  type TrajectoryQueryParams,
  type TrajectorySearchFormValues,
} from '../../data/schema'

interface TrajectorySearchFormProps {
  mapId: string | null
  isSearching: boolean
  hasResults: boolean
  onSearch: (params: TrajectoryQueryParams) => void
  onClear: () => void
  disabled?: boolean
}

/** 时间预设 → 绝对区间（浏览器本地时区，发送前统一转 UTC RFC3339）。 */
function resolveTimeRange(preset: TimeRangePreset): {
  startTime: string
  endTime: string
} {
  const now = new Date()
  let start = new Date()

  switch (preset) {
    case '1h':
      start = new Date(now.getTime() - 60 * 60 * 1000)
      break
    case 'today':
      start = new Date(
        now.getFullYear(),
        now.getMonth(),
        now.getDate(),
        0,
        0,
        0
      )
      break
    case '24h':
      start = new Date(now.getTime() - 24 * 60 * 60 * 1000)
      break
    case '7d':
      start = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000)
      break
  }

  return {
    startTime: start.toISOString(),
    endTime: now.toISOString(),
  }
}

const TIME_PRESET_LABEL_KEY: Record<TimeRangePreset, string> = {
  '1h': 'trajectory.preset1h',
  today: 'trajectory.presetToday',
  '24h': 'trajectory.preset24h',
  '7d': 'trajectory.preset7d',
}

const TIME_PRESETS: TimeRangePreset[] = ['1h', 'today', '24h', '7d']

export function TrajectorySearchForm({
  mapId,
  isSearching,
  hasResults,
  onSearch,
  onClear,
  disabled,
}: TrajectorySearchFormProps) {
  const { t } = useTranslation('spatial')

  const schema = useMemo(() => createTrajectorySearchFormSchema(t), [t])
  const form = useForm<TrajectorySearchFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      targetType: 'vehicle',
      plate: '',
      personId: '',
      timeRange: 'today',
    },
  })

  const targetType = useWatch({ control: form.control, name: 'targetType' })
  const { data: personsData } = useAllPersons()
  const enrolledPersons = personsData?.items ?? []

  const onSubmit = (values: TrajectorySearchFormValues) => {
    if (!mapId) return
    const { startTime, endTime } = resolveTimeRange(values.timeRange)
    onSearch({
      mapId,
      targetType: values.targetType,
      plate: values.targetType === 'vehicle' ? values.plate.trim() : undefined,
      personId: values.targetType === 'person' ? values.personId : undefined,
      startTime,
      endTime,
    })
  }

  const canSearch = Boolean(mapId) && !disabled && !isSearching

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className='flex flex-wrap items-start gap-2 rounded-lg border border-border bg-background/95 p-2 shadow-sm backdrop-blur-sm'
      >
        <FormField
          control={form.control}
          name='targetType'
          render={({ field }) => (
            <FormItem className='space-y-0'>
              <FormControl>
                <Tabs
                  value={field.value}
                  onValueChange={(val) =>
                    field.onChange(val as 'vehicle' | 'person')
                  }
                  className='h-9'
                >
                  <TabsList className='h-9 p-0.5'>
                    <TabsTrigger
                      value='vehicle'
                      className='flex h-8 items-center gap-1.5 px-2.5 text-xs'
                    >
                      <CarFront className='h-3.5 w-3.5' />
                      {t('trajectory.vehicle')}
                    </TabsTrigger>
                    <TabsTrigger
                      value='person'
                      className='flex h-8 items-center gap-1.5 px-2.5 text-xs'
                    >
                      <User className='h-3.5 w-3.5' />
                      {t('trajectory.person')}
                    </TabsTrigger>
                  </TabsList>
                </Tabs>
              </FormControl>
            </FormItem>
          )}
        />

        {targetType === 'vehicle' ? (
          <FormField
            control={form.control}
            name='plate'
            render={({ field }) => (
              <FormItem className='w-44 space-y-1 sm:w-52'>
                <FormControl>
                  <Input
                    {...field}
                    placeholder={t('trajectory.platePlaceholder')}
                    className='h-9 text-xs'
                    disabled={disabled || !mapId}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ) : (
          <FormField
            control={form.control}
            name='personId'
            render={({ field }) => (
              <FormItem className='w-44 space-y-1 sm:w-52'>
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                  disabled={disabled || !mapId}
                >
                  <FormControl>
                    <SelectTrigger className='h-9 text-xs'>
                      <SelectValue
                        placeholder={t('trajectory.selectPersonPlaceholder')}
                      />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {enrolledPersons.map((person) => (
                      <SelectItem
                        key={person.id}
                        value={person.personId || person.id}
                      >
                        {person.name}
                        {person.personId ? ` (${person.personId})` : ''}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />
        )}

        <FormField
          control={form.control}
          name='timeRange'
          render={({ field }) => (
            <FormItem className='w-32 space-y-1'>
              <Select
                value={field.value}
                onValueChange={(val) => field.onChange(val as TimeRangePreset)}
                disabled={disabled || !mapId}
              >
                <FormControl>
                  <SelectTrigger
                    className='h-9 text-xs'
                    aria-label={t('trajectory.timeRange')}
                  >
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  {TIME_PRESETS.map((preset) => (
                    <SelectItem key={preset} value={preset}>
                      {t(TIME_PRESET_LABEL_KEY[preset])}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FormMessage />
            </FormItem>
          )}
        />

        <Button
          type='submit'
          size='sm'
          className='h-9 gap-1.5 px-3'
          disabled={!canSearch}
        >
          {isSearching ? (
            <Loader2 className='h-3.5 w-3.5 animate-spin' />
          ) : (
            <Search className='h-3.5 w-3.5' />
          )}
          {t('trajectory.searchAction')}
        </Button>

        {hasResults && (
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-9 px-2 text-muted-foreground'
            onClick={onClear}
            aria-label={t('trajectory.clearAction')}
            title={t('trajectory.clearAction')}
          >
            <RotateCcw className='h-3.5 w-3.5' />
          </Button>
        )}
      </form>
    </Form>
  )
}
