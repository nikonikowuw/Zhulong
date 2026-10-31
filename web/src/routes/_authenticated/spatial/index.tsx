import { createFileRoute } from '@tanstack/react-router'
import { Spatial } from '@/features/spatial'
import { spatialSearchSchema } from '@/features/spatial/data/schema'

export const Route = createFileRoute('/_authenticated/spatial/')({
  validateSearch: (search) => spatialSearchSchema.parse(search),
  component: Spatial,
})
