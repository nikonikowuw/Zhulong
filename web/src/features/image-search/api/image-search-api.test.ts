import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { imageSearchApi } from './image-search-api'

function mockResponse<T>(
  data: T,
  config: InternalAxiosRequestConfig<unknown>
): AxiosResponse<ApiResponse<T>, unknown> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  }
}

const defaultAdapter = apiClient.defaults.adapter

afterEach(() => {
  apiClient.defaults.adapter = defaultAdapter
})

describe('imageSearchApi', () => {
  it.each([
    {
      name: 'custom options',
      threshold: 0.75,
      topK: 20,
      expectedThreshold: '0.75',
      expectedTopK: '20',
    },
    {
      name: 'zero threshold',
      threshold: 0,
      topK: 50,
      expectedThreshold: '0',
      expectedTopK: '50',
    },
    {
      name: 'default options',
      threshold: undefined,
      topK: undefined,
      expectedThreshold: null,
      expectedTopK: null,
    },
  ])(
    'searchByImage preserves multipart data through Axios with $name',
    async ({ threshold, topK, expectedThreshold, expectedTopK }) => {
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
      // Mock only the transport so Axios still merges defaults and serializes the request.
      const adapter = vi.fn(
        async (config: InternalAxiosRequestConfig<unknown>) =>
          mockResponse(mockData, config)
      )
      apiClient.defaults.adapter = adapter

      const imageBytes = new Uint8Array([0xff, 0xd8, 0x00, 0x80, 0xff, 0xd9])
      const fakeFile = new File([imageBytes], 'test.jpg', {
        type: 'image/jpeg',
      })
      const onUploadProgress = vi.fn()
      const result = await imageSearchApi.searchByImage({
        image: fakeFile,
        threshold,
        topK,
        onUploadProgress,
      })

      expect(result).toEqual(mockData)
      expect(adapter).toHaveBeenCalledTimes(1)
      const [config] = adapter.mock.calls[0]
      expect(config.url).toBe('/persons/search-by-image')
      expect(config.headers.getContentType()).toBe('multipart/form-data')
      expect(config.onUploadProgress).toBe(onUploadProgress)

      const formData = config.data
      if (!(formData instanceof FormData)) {
        throw new Error('Axios must preserve the image upload as FormData')
      }
      expect(formData.get('threshold')).toBe(expectedThreshold)
      expect(formData.get('topK')).toBe(expectedTopK)

      const uploadedImage = formData.get('image')
      if (!(uploadedImage instanceof File)) {
        throw new Error('The image part must remain a File after serialization')
      }
      expect(uploadedImage.name).toBe('test.jpg')
      expect(uploadedImage.type).toBe('image/jpeg')
      expect(new Uint8Array(await uploadedImage.arrayBuffer())).toEqual(
        imageBytes
      )
    }
  )

  it('getFaceImageUrl formats the secure person face image url', () => {
    const url = imageSearchApi.getFaceImageUrl('p-123', 'f-456')
    expect(url).toBe('/api/v1/persons/p-123/faces/f-456/image')
  })
})
