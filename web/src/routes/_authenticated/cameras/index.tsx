import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Cameras } from '@/features/cameras'

const camerasSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
  status: z
    .array(
      z.union([
        z.literal('online'),
        z.literal('offline'),
        z.literal('error'),
        z.literal('unknown'),
      ])
    )
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/cameras/')({
  validateSearch: camerasSearchSchema,
  component: Cameras,
})
