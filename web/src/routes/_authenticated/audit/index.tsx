import { createFileRoute } from '@tanstack/react-router'
import { Audit } from '@/features/audit'
import { auditSearchSchema } from '@/features/audit/data/schema'

export const Route = createFileRoute('/_authenticated/audit/')({
  validateSearch: (search) => auditSearchSchema.parse(search),
  component: Audit,
})
