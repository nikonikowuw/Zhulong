import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Tasks } from '@/features/tasks'

const tasksSearchSchema = z.object({
  view: z.enum(['channel', 'table']).optional().catch('channel'),
  search: z.string().optional().catch(''),
  camera: z.string().optional().catch('all'),
  algorithm: z.string().optional().catch('all'),
  status: z.enum(['all', 'running', 'paused']).optional().catch('all'),
})

export const Route = createFileRoute('/_authenticated/tasks/')({
  validateSearch: tasksSearchSchema,
  component: Tasks,
})
