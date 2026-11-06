import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  MAX_TRIPWIRES,
  type RegionDraft,
  type Tripwire,
} from '../data/region-config'

interface Props {
  draft: RegionDraft
  disabled: boolean
  onChange: (draft: RegionDraft) => void
  onBeforeChange: () => void
}

function TripwireRow({
  line,
  disabled,
  onUpdate,
  onDelete,
}: {
  line: Tripwire
  disabled: boolean
  onUpdate: (line: Tripwire) => void
  onDelete: () => void
}) {
  const { t } = useTranslation('tasks')
  const form = useForm({ values: { name: line.name } })
  return (
    <div className='space-y-2 rounded-md border border-border p-2'>
      <Input
        aria-label={t('region.tripwireName')}
        disabled={disabled}
        {...form.register('name', {
          required: true,
          validate: (value) =>
            value.trim().length > 0 && [...value.trim()].length <= 64,
        })}
        onBlur={() => {
          const name = form.getValues('name').trim()
          if (name && [...name].length <= 64) {
            if (name !== line.name) onUpdate({ ...line, name })
          } else {
            form.setError('name', { type: 'validate' })
          }
        }}
      />
      {form.formState.errors.name && (
        <p role='alert' className='text-xs text-destructive'>
          {t('region.tripwireNameInvalid')}
        </p>
      )}
      <div className='flex items-center gap-2'>
        <Select
          disabled={disabled}
          value={line.direction}
          onValueChange={(value) => {
            if (value === 'a_to_b' || value === 'b_to_a' || value === 'both')
              onUpdate({ ...line, direction: value })
          }}
        >
          <SelectTrigger aria-label={t('region.tripwireDirection')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='a_to_b'>A → B</SelectItem>
            <SelectItem value='b_to_a'>B → A</SelectItem>
            <SelectItem value='both'>A ↔ B</SelectItem>
          </SelectContent>
        </Select>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={disabled}
          onClick={onDelete}
        >
          {t('region.tripwireDelete')}
        </Button>
      </div>
      <p className='font-mono text-xs text-muted-foreground'>
        {line.points
          .map((p) => p.map((v) => v.toFixed(3)).join(', '))
          .join(' → ')}
      </p>
    </div>
  )
}

export function TripwireList({
  draft,
  disabled,
  onChange,
  onBeforeChange,
}: Props) {
  const { t } = useTranslation('tasks')
  const lines = draft.tripwires ?? []
  const update = (tripwires: Tripwire[]) => {
    onBeforeChange()
    onChange({ ...draft, tripwires })
  }
  return (
    <div className='space-y-2'>
      <p className='text-xs font-semibold'>
        {t('region.tripwireTitle')} ({lines.length}/{MAX_TRIPWIRES})
      </p>
      <p className='text-xs text-muted-foreground'>
        {t('region.tripwireHint')}
      </p>
      {lines.map((line) => (
        <TripwireRow
          key={line.id}
          line={line}
          disabled={disabled}
          onUpdate={(next) =>
            update(lines.map((item) => (item.id === line.id ? next : item)))
          }
          onDelete={() => update(lines.filter((item) => item.id !== line.id))}
        />
      ))}
    </div>
  )
}
