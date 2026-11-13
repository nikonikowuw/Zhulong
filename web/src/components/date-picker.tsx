import { format } from 'date-fns'
import { Calendar as CalendarIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getDateFnsLocale } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

type DatePickerProps = {
  selected: Date | undefined
  onSelect: (date: Date | undefined) => void
  placeholder?: string
}

export function DatePicker({
  selected,
  onSelect,
  placeholder,
}: DatePickerProps) {
  const { t, i18n } = useTranslation()
  const locale = getDateFnsLocale(i18n.resolvedLanguage || i18n.language)
  const resolvedPlaceholder =
    placeholder ?? t('components.datePicker.placeholder')
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant='outline'
          data-empty={!selected}
          className='w-60 justify-start text-start font-normal data-[empty=true]:text-muted-foreground'
        >
          {selected ? (
            format(selected, 'PP', { locale })
          ) : (
            <span>{resolvedPlaceholder}</span>
          )}
          <CalendarIcon className='ms-auto h-4 w-4 opacity-50' />
        </Button>
      </PopoverTrigger>
      <PopoverContent className='w-auto p-0'>
        <Calendar
          mode='single'
          captionLayout='dropdown'
          locale={locale}
          selected={selected}
          onSelect={onSelect}
          disabled={(date: Date) =>
            date > new Date() || date < new Date('1900-01-01')
          }
        />
      </PopoverContent>
    </Popover>
  )
}
