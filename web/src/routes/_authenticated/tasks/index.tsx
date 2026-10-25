import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Tasks } from '@/features/tasks'

const tasksSearchSchema = z.object({
  view: z.enum(['channel', 'table']).optional().catch('channel'),
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
  camera: z.array(z.string()).optional().catch([]),
  algorithm: z.array(z.string()).optional().catch([]),
  status: z
    .array(z.enum(['running', 'paused']))
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/tasks/')({
  validateSearch: tasksSearchSchema,
  component: Tasks,
})
