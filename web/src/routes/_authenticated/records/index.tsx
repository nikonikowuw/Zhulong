import { createFileRoute } from '@tanstack/react-router'
import { Records } from '@/features/records'
import { recordsSearchSchema } from '@/features/records/data/schema'

export const Route = createFileRoute('/_authenticated/records/')({
  validateSearch: (search) => recordsSearchSchema.parse(search),
  component: Records,
})
