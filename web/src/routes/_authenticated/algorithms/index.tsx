import { createFileRoute } from '@tanstack/react-router'
import { Algorithms } from '@/features/algorithms'

export const Route = createFileRoute('/_authenticated/algorithms/')({
  component: Algorithms,
})
