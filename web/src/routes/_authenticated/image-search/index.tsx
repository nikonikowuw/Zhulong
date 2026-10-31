import { createFileRoute } from '@tanstack/react-router'
import { ImageSearch } from '@/features/image-search'

export const Route = createFileRoute('/_authenticated/image-search/')({
  component: ImageSearch,
})
