import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Persons } from '@/features/persons'

const personsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
})

export const Route = createFileRoute('/_authenticated/persons/')({
  validateSearch: personsSearchSchema,
  component: Persons,
})
