import { useState, useEffect, useMemo, useCallback } from 'react'
import { AIResultService } from '../services/ai-result-service'
import { type RecognitionRecordItem } from '../types/ai-result'

export type RecordStatusFilter = 'all' | 'matched' | 'unmatched'

export function useRecognitionRecords() {
  const [records, setRecords] = useState<RecognitionRecordItem[]>(() =>
    AIResultService.getInstance().getRecentRecords()
  )
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState<RecordStatusFilter>('all')

  useEffect(() => {
    const service = AIResultService.getInstance()
    const unsubscribe = service.subscribeRecords((newRecord) => {
      setRecords((prev) => [newRecord, ...prev.slice(0, 99)])
    })

    return () => {
      unsubscribe()
    }
  }, [])

  const clearRecords = useCallback(() => {
    AIResultService.getInstance().clearRecords()
    setRecords([])
  }, [])

  const filteredRecords = useMemo(() => {
    let result = records

    if (statusFilter === 'matched') {
      result = result.filter((r) => r.matched)
    } else if (statusFilter === 'unmatched') {
      result = result.filter((r) => !r.matched)
    }

    if (searchQuery.trim()) {
      const q = searchQuery.trim().toLowerCase()
      result = result.filter(
        (r) =>
          r.personName.toLowerCase().includes(q) ||
          r.personId.toLowerCase().includes(q) ||
          r.cameraId.toLowerCase().includes(q)
      )
    }

    return result
  }, [records, statusFilter, searchQuery])

  return {
    records: filteredRecords,
    totalCount: records.length,
    searchQuery,
    setSearchQuery,
    statusFilter,
    setStatusFilter,
    clearRecords,
  }
}
