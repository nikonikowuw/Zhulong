import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { imageSearchApi } from './image-search-api'

function mockResponse<T>(data: T, status = 200): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('imageSearchApi', () => {
  it('searchByImage posts multipart form data and parses matches', async () => {
    const mockData = {
      matches: [
        {
          personId: 'p-1',
          personExternalId: 'EXT-001',
          personName: 'Alice',
          matchedFaceId: 'f-1',
          similarity: 0.92,
        },
      ],
    }
    const postSpy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(mockResponse(mockData))

    const fakeFile = new File(['mock content'], 'test.jpg', {
      type: 'image/jpeg',
    })
    const result = await imageSearchApi.searchByImage({
      image: fakeFile,
      threshold: 0.75,
      topK: 20,
    })

    expect(result).toEqual(mockData)
    expect(postSpy).toHaveBeenCalledTimes(1)
    const [url, formData, config] = postSpy.mock.calls[0]
    expect(url).toBe('/persons/search-by-image')
    expect(formData).toBeInstanceOf(FormData)
    expect((formData as FormData).get('threshold')).toBe('0.75')
    expect((formData as FormData).get('topK')).toBe('20')
    expect(config?.headers?.['Content-Type']).toBeUndefined()
  })

  it('getFaceImageUrl formats the secure person face image url', () => {
    const url = imageSearchApi.getFaceImageUrl('p-123', 'f-456')
    expect(url).toBe('/api/v1/persons/p-123/faces/f-456/image')
  })
})
