import { useTranslation } from 'react-i18next'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { POPULAR_TIMEZONES } from '../data/timezones'

interface TimezoneSelectProps {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}

export function TimezoneSelect({
  value,
  onChange,
  disabled,
}: TimezoneSelectProps) {
  const { t } = useTranslation('time')

  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className='w-full'>
        <SelectValue placeholder={t('timezone.placeholder')} />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectLabel>{t('timezone.groupAsia')}</SelectLabel>
          {POPULAR_TIMEZONES.slice(0, 7).map((tz) => (
            <SelectItem key={tz.value} value={tz.value}>
              {tz.label}
            </SelectItem>
          ))}
        </SelectGroup>
        <SelectGroup>
          <SelectLabel>{t('timezone.groupIntl')}</SelectLabel>
          {POPULAR_TIMEZONES.slice(7).map((tz) => (
            <SelectItem key={tz.value} value={tz.value}>
              {tz.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}
