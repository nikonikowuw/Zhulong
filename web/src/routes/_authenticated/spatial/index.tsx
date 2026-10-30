import { createFileRoute } from '@tanstack/react-router'
import { Spatial } from '@/features/spatial'

export const Route = createFileRoute('/_authenticated/spatial/')({
  component: Spatial,
})
