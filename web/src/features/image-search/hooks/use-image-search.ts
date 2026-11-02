import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchResponse } from '../data/schema'

const DEFAULT_IMAGE_SEARCH_THRESHOLD = 0.7
const DEFAULT_IMAGE_SEARCH_TOP_K = 10

export function useImageSearch() {
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [threshold, setThreshold] = useState<number>(
    DEFAULT_IMAGE_SEARCH_THRESHOLD
  )
  const [topK, setTopK] = useState<number>(DEFAULT_IMAGE_SEARCH_TOP_K)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null)

  const previewUrlRef = useRef<string | null>(null)

  // 卸载时回收可能存在的 ObjectURL
  useEffect(() => {
    return () => {
      if (previewUrlRef.current) {
        URL.revokeObjectURL(previewUrlRef.current)
      }
    }
  }, [])

  const {
    mutate: executeSearch,
    reset: resetMutation,
    isPending: isSearching,
    isSuccess,
    isError,
    error,
    data: searchData,
  } = useMutation<ImageSearchResponse, Error, void>({
    mutationFn: async () => {
      if (!selectedFile) {
        throw new Error('No image selected')
      }
      setUploadProgress(0)
      return imageSearchApi.searchByImage({
        image: selectedFile,
        threshold,
        topK,
        onUploadProgress: (progressEvent) => {
          if (progressEvent.total) {
            const percent = Math.round(
              (progressEvent.loaded * 100) / progressEvent.total
            )
            setUploadProgress(percent)
          }
        },
      })
    },
    onSettled: () => {
      setUploadProgress(null)
    },
  })

  // 变更或清除图片时释放旧 ObjectURL 并联动重置旧检索结果，避免图文脱节
  const setFile = useCallback(
    (file: File | null) => {
      if (previewUrlRef.current) {
        URL.revokeObjectURL(previewUrlRef.current)
        previewUrlRef.current = null
      }
      setSelectedFile(file)
      if (file) {
        const url = URL.createObjectURL(file)
        previewUrlRef.current = url
        setPreviewUrl(url)
      } else {
        setPreviewUrl(null)
      }
      resetMutation()
    },
    [resetMutation]
  )

  const reset = useCallback(() => {
    setFile(null)
    setThreshold(DEFAULT_IMAGE_SEARCH_THRESHOLD)
    setTopK(DEFAULT_IMAGE_SEARCH_TOP_K)
  }, [setFile])

  return {
    selectedFile,
    previewUrl,
    threshold,
    setThreshold,
    topK,
    setTopK,
    uploadProgress,
    setFile,
    reset,
    search: () => executeSearch(),
    isSearching,
    isSuccess,
    isError,
    error,
    results: searchData?.matches ?? null,
  }
}
