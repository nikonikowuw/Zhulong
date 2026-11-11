import { createFileRoute } from '@tanstack/react-router'
import { Persons } from '@/features/persons'
import { personsSearchSchema } from '@/features/persons/search-schema'

export const Route = createFileRoute('/_authenticated/persons/')({
  validateSearch: personsSearchSchema,
  component: Persons,
})
