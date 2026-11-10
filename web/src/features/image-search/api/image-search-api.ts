import { apiClient, type ApiResponse } from '@/lib/api-client'
import { getGalleryImageUrl } from '@/lib/media-url'
import {
  imageSearchResponseSchema,
  type ImageSearchParams,
  type ImageSearchResponse,
} from '../data/schema'

export const imageSearchApi = {
  async searchByImage(params: ImageSearchParams): Promise<ImageSearchResponse> {
    const formData = new FormData()
    formData.append('image', params.image)
    if (typeof params.threshold === 'number') {
      formData.append('threshold', params.threshold.toString())
    }
    if (typeof params.topK === 'number') {
      formData.append('topK', params.topK.toString())
    }

    const resp = await apiClient.post<ApiResponse<ImageSearchResponse>>(
      '/persons/search-by-image',
      formData,
      {
        headers: { 'Content-Type': 'multipart/form-data' },
        onUploadProgress: params.onUploadProgress,
      }
    )

    return imageSearchResponseSchema.parse(resp.data.data)
  },

  /** 底库图片 URL 统一由 `@/lib/media-url` 生成，避免各特性各拼一份。 */
  getFaceImageUrl(personId: string, faceId: string): string {
    return getGalleryImageUrl(personId, faceId)
  },
}
