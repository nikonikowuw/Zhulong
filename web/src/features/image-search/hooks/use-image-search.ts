import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { imageSearchApi } from '../api/image-search-api'
import { type ImageSearchResponse } from '../data/schema'

export function useImageSearch() {
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [threshold, setThreshold] = useState<number>(0.7)
  const [topK, setTopK] = useState<number>(10)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null)

  const previewUrlRef = useRef<string | null>(null)

  const clearPreview = useCallback(() => {
    if (previewUrlRef.current) {
      URL.revokeObjectURL(previewUrlRef.current)
      previewUrlRef.current = null
      setPreviewUrl(null)
    }
  }, [])

  const setFile = useCallback(
    (file: File | null) => {
      clearPreview()
      setSelectedFile(file)
      if (file) {
        const url = URL.createObjectURL(file)
        previewUrlRef.current = url
        setPreviewUrl(url)
      }
    },
    [clearPreview]
  )

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

  const reset = useCallback(() => {
    setFile(null)
    resetMutation()
  }, [setFile, resetMutation])

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
    search: executeSearch,
    isSearching,
    isSuccess,
    isError,
    error,
    results: searchData?.matches ?? null,
  }
}
