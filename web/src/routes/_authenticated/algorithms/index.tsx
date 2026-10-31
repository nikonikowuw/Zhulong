import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Algorithms } from '@/features/algorithms'

const algorithmsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
  taskType: z
    .array(
      z.union([
        z.literal('detection'),
        z.literal('classification'),
        z.literal('person_structure'),
        z.literal('vehicle_structure'),
      ])
    )
    .optional()
    .catch([]),
  status: z
    .array(
      z.union([
        z.literal('ACTIVE'),
        z.literal('DISABLED'),
        z.literal('STARTING'),
        z.literal('RUNNING'),
        z.literal('CRASHED'),
        z.literal('CRASH_LOOP'),
        z.literal('ROLLING_BACK'),
      ])
    )
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/algorithms/')({
  validateSearch: algorithmsSearchSchema,
  component: Algorithms,
})
