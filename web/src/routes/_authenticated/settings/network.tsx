import { createFileRoute } from '@tanstack/react-router'
import { SettingsNetwork } from '@/features/settings/network'

export const Route = createFileRoute('/_authenticated/settings/network')({
  component: SettingsNetwork,
})
