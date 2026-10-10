import { createFileRoute } from '@tanstack/react-router'
import { SettingsStorage } from '@/features/settings/storage'

export const Route = createFileRoute('/_authenticated/settings/storage')({
  component: SettingsStorage,
})
