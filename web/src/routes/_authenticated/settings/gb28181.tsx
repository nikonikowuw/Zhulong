import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { SettingsGb28181 } from '@/features/gb28181'

const gb28181SearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  search: z.string().optional().catch(''),
  status: z
    .array(z.union([z.literal('online'), z.literal('offline')]))
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/settings/gb28181')({
  validateSearch: gb28181SearchSchema,
  component: SettingsGb28181,
})
