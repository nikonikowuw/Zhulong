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

export function TimezoneSelect({ value, onChange, disabled }: TimezoneSelectProps) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className='w-full'>
        <SelectValue placeholder='选择时区' />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          <SelectLabel>亚洲及常用时区</SelectLabel>
          {POPULAR_TIMEZONES.slice(0, 7).map((tz) => (
            <SelectItem key={tz.value} value={tz.value}>
              {tz.label}
            </SelectItem>
          ))}
        </SelectGroup>
        <SelectGroup>
          <SelectLabel>国际标准与欧美时区</SelectLabel>
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
