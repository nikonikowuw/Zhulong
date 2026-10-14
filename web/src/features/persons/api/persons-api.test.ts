import type { AxiosResponse } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { personsApi } from './persons-api'

function mockResponse<T>(data: T, status = 200): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('personsApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('getPersons calls GET /persons with pagination params', async () => {
    const mockData = {
      items: [
        {
          id: 'p-01',
          personId: 'EMP001',
          name: 'Alice',
          createdAt: '2026-10-14T00:00:00Z',
          updatedAt: '2026-10-14T00:00:00Z',
          faces: [],
        },
      ],
      total: 1,
      page: 1,
      pageSize: 10,
    }

    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockData))

    const res = await personsApi.getPersons({ page: 2, pageSize: 20, search: 'Alice' })
    expect(res).toEqual(mockData)
    expect(spy).toHaveBeenCalledWith('/persons', {
      params: { page: 2, pageSize: 20, search: 'Alice' },
    })
  })

  it('getPerson calls GET /persons/:id', async () => {
    const mockPerson = {
      id: 'p-01',
      personId: 'EMP001',
      name: 'Alice',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T00:00:00Z',
      faces: [],
    }

    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockPerson))

    const res = await personsApi.getPerson('p-01')
    expect(res).toEqual(mockPerson)
    expect(spy).toHaveBeenCalledWith('/persons/p-01')
  })

  it('createPerson calls POST /persons', async () => {
    const payload = { name: 'Bob', personId: 'EMP002' }
    const created = {
      id: 'p-02',
      personId: 'EMP002',
      name: 'Bob',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T00:00:00Z',
      faces: [],
    }

    const spy = vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(created, 201))

    const res = await personsApi.createPerson(payload)
    expect(res).toEqual(created)
    expect(spy).toHaveBeenCalledWith('/persons', payload)
  })

  it('updatePerson calls PUT /persons/:id', async () => {
    const payload = { name: 'Bob Dylan' }
    const updated = {
      id: 'p-02',
      personId: 'EMP002',
      name: 'Bob Dylan',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T01:00:00Z',
      faces: [],
    }

    const spy = vi.spyOn(apiClient, 'put').mockResolvedValueOnce(mockResponse(updated))

    const res = await personsApi.updatePerson('p-02', payload)
    expect(res).toEqual(updated)
    expect(spy).toHaveBeenCalledWith('/persons/p-02', payload)
  })

  it('deletePerson calls DELETE /persons/:id', async () => {
    const spy = vi
      .spyOn(apiClient, 'delete')
      .mockResolvedValueOnce(mockResponse({ deleted: true }))

    await personsApi.deletePerson('p-02')
    expect(spy).toHaveBeenCalledWith('/persons/p-02')
  })

  it('listFaces calls GET /persons/:id/faces', async () => {
    const mockFaces = [
      {
        id: 'f-01',
        ownerRowId: 'p-01',
        imagePath: '/data/faces/alice.jpg',
        createdAt: '2026-10-14T00:00:00Z',
      },
    ]

    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockFaces))

    const res = await personsApi.listFaces('p-01')
    expect(res).toEqual(mockFaces)
    expect(spy).toHaveBeenCalledWith('/persons/p-01/faces')
  })

  it('addFace uploads file with multipart/form-data', async () => {
    const file = new File(['dummy-bytes'], 'avatar.png', { type: 'image/png' })
    const createdFace = {
      id: 'f-02',
      ownerRowId: 'p-01',
      imagePath: '/data/faces/avatar.png',
      createdAt: '2026-10-14T00:00:00Z',
    }

    const spy = vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(createdFace, 201))

    const res = await personsApi.addFace('p-01', file)
    expect(res).toEqual(createdFace)
    expect(spy).toHaveBeenCalledWith(
      '/persons/p-01/faces',
      expect.any(FormData),
      { headers: { 'Content-Type': 'multipart/form-data' } }
    )
  })

  it('getFaceImageUrl returns image URL path', () => {
    const url = personsApi.getFaceImageUrl('p-01', 'f-01')
    expect(url).toBe('/api/v1/persons/p-01/faces/f-01/image')
  })

  it('deleteFace calls DELETE /persons/:id/faces/:face_id', async () => {
    const spy = vi
      .spyOn(apiClient, 'delete')
      .mockResolvedValueOnce(mockResponse({ deleted: true }))

    await personsApi.deleteFace('p-01', 'f-01')
    expect(spy).toHaveBeenCalledWith('/persons/p-01/faces/f-01')
  })
})
