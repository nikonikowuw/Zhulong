import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Persons } from '@/features/persons'

const faceStatusFilterSchema = z.enum(['enrolled', 'missing'])

const personsSearchSchema = z.object({
  view: z.enum(['table', 'grid']).optional().catch('table'),
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
  personId: z.string().optional().catch(undefined),
  faceStatus: z
    .union([z.array(faceStatusFilterSchema), faceStatusFilterSchema])
    .optional()
    .catch([])
    .transform((val) => {
      if (!val) return []
      return Array.isArray(val) ? val : [val]
    }),
})

export const Route = createFileRoute('/_authenticated/persons/')({
  validateSearch: personsSearchSchema,
  component: Persons,
})
