import { useEffect, useRef, useState } from 'react'
import { Input } from '@/components/ui/input'

const PERSON_ID_DEBOUNCE_MS = 300

export interface PersonIdSearchInputProps {
  value: string
  placeholder: string
  onCommit: (next?: string) => void
}

export function PersonIdSearchInput({
  value,
  placeholder,
  onCommit,
}: PersonIdSearchInputProps) {
  const [draft, setDraft] = useState(value)
  const [syncedValue, setSyncedValue] = useState(value)
  const onCommitRef = useRef(onCommit)

  useEffect(() => {
    onCommitRef.current = onCommit
  }, [onCommit])

  if (value !== syncedValue) {
    setSyncedValue(value)
    setDraft(value)
  }

  useEffect(() => {
    const normalizedDraft = draft.trim()
    const normalizedValue = value.trim()
    if (normalizedDraft === normalizedValue) return
    const timer = setTimeout(() => {
      onCommitRef.current(normalizedDraft || undefined)
    }, PERSON_ID_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [draft, value])

  return (
    <Input
      placeholder={placeholder}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          onCommitRef.current(draft.trim() || undefined)
        }
      }}
      className='h-8 w-37.5 text-xs lg:w-62.5'
    />
  )
}
