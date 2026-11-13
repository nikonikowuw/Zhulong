import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { errorMessageOf } from '@/lib/handle-server-error'
import { validateFaceImageFile } from '../persons-helpers'
import {
  PERSONS_QUERY_KEY,
  personFacesQueryKey,
  useAddFace,
} from './use-persons'

interface UseFaceBatchUploadOptions {
  personId?: string
  hasExistingFaces: boolean
  onFirstFaceEnrolled?: (faceId: string) => void
}

/**
 * 批量人脸底库文件校验与串行上传编排。
 *
 * 整批上传完成后统一 invalidate，避免逐张触发列表与详情重请求风暴。
 */
export function useFaceBatchUpload({
  personId,
  hasExistingFaces,
  onFirstFaceEnrolled,
}: UseFaceBatchUploadOptions) {
  const { t } = useTranslation('persons')
  const queryClient = useQueryClient()
  const addFaceBatchMutation = useAddFace({
    suppressGlobalToast: true,
    suppressInvalidate: true,
  })

  const [isDragOver, setIsDragOver] = useState(false)
  const [isUploadingBatch, setIsUploadingBatch] = useState(false)
  const [batchProgress, setBatchProgress] = useState<{
    current: number
    total: number
  } | null>(null)

  const uploadRef = useRef({
    mutate: addFaceBatchMutation.mutateAsync,
    hasExistingFaces,
    onFirstFaceEnrolled,
  })
  useEffect(() => {
    uploadRef.current = {
      mutate: addFaceBatchMutation.mutateAsync,
      hasExistingFaces,
      onFirstFaceEnrolled,
    }
  }, [addFaceBatchMutation.mutateAsync, hasExistingFaces, onFirstFaceEnrolled])

  const uploadFiles = useCallback(
    async (files: FileList | File[]) => {
      if (!personId || files.length === 0) return

      const validFiles: File[] = []
      for (let i = 0; i < files.length; i++) {
        const file = files[i]
        const problem = validateFaceImageFile(file)
        if (problem) {
          toast.error(
            `${file.name}: ${
              problem === 'invalidType'
                ? t('validation.invalidFileType')
                : t('validation.fileTooLarge')
            }`
          )
          continue
        }
        validFiles.push(file)
      }

      if (validFiles.length === 0) return

      setIsUploadingBatch(true)
      setBatchProgress({ current: 0, total: validFiles.length })

      const {
        mutate,
        hasExistingFaces: hadFaces,
        onFirstFaceEnrolled: notifyFirst,
      } = uploadRef.current

      let successCount = 0
      for (let i = 0; i < validFiles.length; i++) {
        const file = validFiles[i]
        setBatchProgress({ current: i + 1, total: validFiles.length })
        try {
          const enrolled = await mutate({ personId, data: file })
          if (!hadFaces && i === 0) {
            notifyFirst?.(enrolled.id)
          }
          successCount++
        } catch (err: unknown) {
          toast.error(`${file.name}: ${errorMessageOf(err)}`)
        }
      }

      setIsUploadingBatch(false)
      setBatchProgress(null)

      if (successCount > 0) {
        void queryClient.invalidateQueries({
          queryKey: personFacesQueryKey(personId),
        })
        void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
        toast.success(
          successCount === validFiles.length
            ? t('notifications.addFaceSuccess')
            : t('notifications.addFacePartial', {
                success: successCount,
                failed: validFiles.length - successCount,
              })
        )
      }
    },
    [personId, t, queryClient]
  )

  return {
    isDragOver,
    setIsDragOver,
    isUploadingBatch,
    batchProgress,
    uploadFiles,
  }
}
