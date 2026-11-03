import { createFileRoute } from '@tanstack/react-router'
import { APITokensPage } from '@/features/api-tokens'

export const Route = createFileRoute('/_authenticated/settings/api-tokens')({
  component: APITokensPage,
})
