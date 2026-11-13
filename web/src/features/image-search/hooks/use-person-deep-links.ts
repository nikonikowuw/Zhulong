import { useCallback } from 'react'
import { useNavigate } from '@tanstack/react-router'

export function usePersonDeepLinks(
  personId: string | null | undefined,
  onBeforeNavigate?: () => void
) {
  const navigate = useNavigate()

  const goRecords = useCallback(() => {
    if (!personId) return
    onBeforeNavigate?.()
    void navigate({
      to: '/records',
      search: {
        tab: 'recognitions',
        view: 'table',
        page: 1,
        pageSize: 20,
        personId,
      },
    })
  }, [navigate, onBeforeNavigate, personId])

  const goSpatial = useCallback(() => {
    if (!personId) return
    onBeforeNavigate?.()
    void navigate({
      to: '/spatial',
      search: {
        targetType: 'person',
        personId,
      },
    })
  }, [navigate, onBeforeNavigate, personId])

  const goProfile = useCallback(() => {
    if (!personId) return
    onBeforeNavigate?.()
    void navigate({
      to: '/persons',
      search: {
        personId,
      },
    })
  }, [navigate, onBeforeNavigate, personId])

  return { goRecords, goSpatial, goProfile }
}
