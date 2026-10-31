import { apiClient, type ApiResponse } from '@/lib/api-client'
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
        headers: {
          'Content-Type': 'multipart/form-data',
        },
        onUploadProgress: params.onUploadProgress,
      }
    )

    return imageSearchResponseSchema.parse(resp.data.data)
  },

  getFaceImageUrl(personId: string, faceId: string): string {
    return `/api/v1/persons/${encodeURIComponent(personId)}/faces/${encodeURIComponent(faceId)}/image`
  },
}
