import { createFileRoute } from '@tanstack/react-router'
import { AlertPushPage } from '@/features/alert-push'

export const Route = createFileRoute('/_authenticated/settings/alert-push')({
  component: AlertPushPage,
})
