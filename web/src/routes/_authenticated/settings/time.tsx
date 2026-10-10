import { createFileRoute } from '@tanstack/react-router'
import { SettingsTime } from '@/features/settings/time'

export const Route = createFileRoute('/_authenticated/settings/time')({
  component: SettingsTime,
})
