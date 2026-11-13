import { useCallback, useEffect, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { imageSearchApi } from '../api/image-search-api'
import {
  DEFAULT_IMAGE_SEARCH_THRESHOLD,
  DEFAULT_IMAGE_SEARCH_TOP_K,
} from '../data/match-utils'
import { type ImageSearchResponse } from '../data/schema'

interface SearchVariables {
  file: File
  threshold: number
  topK: number
}

interface SearchOverrides {
  threshold?: number
  topK?: number
}

export function useImageSearch() {
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [threshold, setThreshold] = useState<number>(
    DEFAULT_IMAGE_SEARCH_THRESHOLD
  )
  const [topK, setTopK] = useState<number>(DEFAULT_IMAGE_SEARCH_TOP_K)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null)

  const previewUrlRef = useRef<string | null>(null)

  // 组件卸载时释放最终可能残留的 ObjectURL
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
    variables: searchedVariables,
    isPending: isSearching,
    isSuccess,
    isError,
    error,
    data: searchData,
  } = useMutation<ImageSearchResponse, Error, SearchVariables>({
    // 右侧结果面板自带完整的错误态展示与重试入口，抑制全局 MutationCache Toast 防止双重报错
    meta: { suppressGlobalToast: true },
    mutationFn: async ({
      file,
      threshold: searchThreshold,
      topK: searchTopK,
    }) => {
      setUploadProgress(0)
      return imageSearchApi.searchByImage({
        image: file,
        threshold: searchThreshold,
        topK: searchTopK,
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

  // 变更或清除图片时即时释放旧 ObjectURL，避免图文脱节与级联渲染
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

  const search = useCallback(
    (overrides?: SearchOverrides) => {
      if (!selectedFile) return
      const nextThreshold = overrides?.threshold ?? threshold
      const nextTopK = overrides?.topK ?? topK
      if (overrides?.threshold !== undefined) {
        setThreshold(overrides.threshold)
      }
      if (overrides?.topK !== undefined) {
        setTopK(overrides.topK)
      }
      executeSearch({
        file: selectedFile,
        threshold: nextThreshold,
        topK: nextTopK,
      })
    },
    [executeSearch, selectedFile, threshold, topK]
  )

  return {
    selectedFile,
    previewUrl,
    threshold,
    setThreshold,
    topK,
    setTopK,
    appliedThreshold: searchData ? searchedVariables?.threshold : undefined,
    appliedTopK: searchedVariables?.topK,
    uploadProgress,
    setFile,
    reset,
    search,
    isSearching,
    isSuccess,
    isError,
    error,
    results: searchData?.matches ?? null,
  }
}
