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
          source: 'LOCAL' as const,
          sourceName: '',
          createdAt: '2026-10-14T00:00:00Z',
          updatedAt: '2026-10-14T00:00:00Z',
          faces: [],
        },
      ],
      total: 1,
      page: 1,
      pageSize: 10,
    }

    const spy = vi
      .spyOn(apiClient, 'get')
      .mockResolvedValueOnce(mockResponse(mockData))

    const res = await personsApi.getPersons({
      page: 2,
      pageSize: 20,
      search: 'Alice',
      faceStatus: 'missing',
    })
    expect(res).toEqual(mockData)
    expect(spy).toHaveBeenCalledWith('/persons', {
      params: { page: 2, pageSize: 20, search: 'Alice', faceStatus: 'missing' },
    })
  })

  it('previewImport uploads the CSV file as multipart form data', async () => {
    const preview = {
      items: [
        {
          line: 2,
          name: 'Alice',
          personId: 'EMP001',
          canImport: true,
          issues: [],
        },
      ],
      total: 1,
      importable: 1,
      failed: 0,
    }
    const file = new File(['name,personId\nAlice,EMP001\n'], 'people.csv', {
      type: 'text/csv',
    })
    const spy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(mockResponse(preview))

    await expect(personsApi.previewImport(file)).resolves.toEqual(preview)
    // 不显式设置 Content-Type：浏览器必须自己写 multipart boundary，
    // 手写成不带 boundary 的 multipart/form-data 在部分网关下会被解析成空体。
    expect(spy).toHaveBeenCalledWith(
      '/persons/import/preview',
      expect.any(FormData)
    )
  })

  it('commitImport uploads the same CSV file as multipart form data', async () => {
    const result = {
      items: [
        {
          line: 2,
          name: 'Alice',
          personId: 'EMP001',
          canImport: true,
          imported: true,
          issues: [],
        },
      ],
      total: 1,
      imported: 1,
      failed: 0,
    }
    const file = new File(['name,personId\nAlice,EMP001\n'], 'people.csv', {
      type: 'text/csv',
    })
    const spy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(mockResponse(result))

    await expect(personsApi.commitImport(file)).resolves.toEqual(result)
    expect(spy).toHaveBeenCalledWith(
      '/persons/import/commit',
      expect.any(FormData)
    )
  })

  it('getPerson calls GET /persons/:id', async () => {
    const mockPerson = {
      id: 'p-01',
      personId: 'EMP001',
      name: 'Alice',
      source: 'LOCAL' as const,
      sourceName: '',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T00:00:00Z',
      faces: [],
    }

    const spy = vi
      .spyOn(apiClient, 'get')
      .mockResolvedValueOnce(mockResponse(mockPerson))

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
      source: 'LOCAL' as const,
      sourceName: '',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T00:00:00Z',
      faces: [],
    }

    const spy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(mockResponse(created, 201))

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
      source: 'LOCAL' as const,
      sourceName: '',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T01:00:00Z',
      faces: [],
    }

    const spy = vi
      .spyOn(apiClient, 'put')
      .mockResolvedValueOnce(mockResponse(updated))

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

  it('listFaces calls GET /persons/:id/faces and unwraps the paginated envelope', async () => {
    const mockFaces = [
      {
        id: 'f-01',
        ownerRowId: 'p-01',
        imagePath: '/data/faces/alice.jpg',
        createdAt: '2026-10-14T00:00:00Z',
      },
    ]

    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse({
        items: mockFaces,
        total: 1,
        page: 1,
        pageSize: 100,
      })
    )

    const res = await personsApi.listFaces('p-01')
    expect(res.items).toEqual(mockFaces)
    expect(res.total).toBe(1)
    expect(spy).toHaveBeenCalledWith('/persons/p-01/faces', {
      params: { page: 1, pageSize: 20 },
    })
  })

  it('addFace uploads file with multipart/form-data', async () => {
    const file = new File(['dummy-bytes'], 'avatar.png', { type: 'image/png' })
    const createdFace = {
      id: 'f-02',
      ownerRowId: 'p-01',
      imagePath: '/data/faces/avatar.png',
      createdAt: '2026-10-14T00:00:00Z',
    }

    const spy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(mockResponse(createdFace, 201))

    const res = await personsApi.addFace('p-01', file)
    expect(res).toEqual(createdFace)
    expect(spy).toHaveBeenCalledWith(
      '/persons/p-01/faces',
      expect.any(FormData)
    )
  })

  it('getFaceImageUrl returns image URL path', () => {
    const url = personsApi.getFaceImageUrl('p-01', 'f-01')
    expect(url).toBe('/api/v1/persons/p-01/faces/f-01/image')
  })

  it('getFaceImageUrl percent-encodes ids so a slash cannot escape the path', () => {
    const url = personsApi.getFaceImageUrl('p/1', 'f 2')
    expect(url).toBe('/api/v1/persons/p%2F1/faces/f%202/image')
  })

  it('deleteFace calls DELETE /persons/:id/faces/:face_id', async () => {
    const spy = vi
      .spyOn(apiClient, 'delete')
      .mockResolvedValueOnce(mockResponse({ deleted: true }))

    await personsApi.deleteFace('p-01', 'f-01')
    expect(spy).toHaveBeenCalledWith('/persons/p-01/faces/f-01')
  })

  it('setPrimaryFace calls PUT /persons/:id/primary-face', async () => {
    const updated = {
      id: 'p-01',
      personId: 'EMP001',
      name: 'Alice',
      primaryFaceId: 'f-02',
      source: 'LOCAL' as const,
      sourceName: '',
      createdAt: '2026-10-14T00:00:00Z',
      updatedAt: '2026-10-14T01:00:00Z',
      faces: [],
    }

    const spy = vi
      .spyOn(apiClient, 'put')
      .mockResolvedValueOnce(mockResponse(updated))

    const res = await personsApi.setPrimaryFace('p-01', 'f-02')
    expect(res).toEqual(updated)
    expect(spy).toHaveBeenCalledWith('/persons/p-01/primary-face', {
      faceId: 'f-02',
    })
  })

  it('exportManifest downloads the CSV named by Content-Disposition', async () => {
    vi.useFakeTimers()
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {})
    const createUrlSpy = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue('blob:manifest-url')
    const revokeUrlSpy = vi
      .spyOn(URL, 'revokeObjectURL')
      .mockImplementation(() => {})

    let downloadedName = ''
    vi.spyOn(HTMLAnchorElement.prototype, 'download', 'set').mockImplementation(
      (value: string) => {
        downloadedName = value
      }
    )

    const blob = new Blob(['\uFEFFname,personId'], { type: 'text/csv' })
    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce({
      data: blob,
      status: 200,
      statusText: 'OK',
      headers: {
        'content-disposition':
          'attachment; filename="person_photo_manifest_missing_20260101_000000.csv"',
      },
      config: {} as AxiosResponse['config'],
    })

    await personsApi.exportManifest({
      search: ' alice ',
      faceStatus: 'missing',
    })

    expect(spy).toHaveBeenCalledWith('/persons/export', {
      params: { search: 'alice', faceStatus: 'missing' },
      responseType: 'blob',
    })
    expect(createUrlSpy).toHaveBeenCalledWith(blob)
    expect(clickSpy).toHaveBeenCalled()
    expect(downloadedName).toBe(
      'person_photo_manifest_missing_20260101_000000.csv'
    )
    // 立即 revoke 会让 Safari/Firefox 在下载真正启动前丢掉大 Blob，
    // 必须延后 —— 与 `@/lib/csv` 的 triggerDownload 保持同一语义。
    expect(revokeUrlSpy).not.toHaveBeenCalled()
    vi.advanceTimersByTime(10_000)
    expect(revokeUrlSpy).toHaveBeenCalledWith('blob:manifest-url')
    vi.useRealTimers()
  })

  it('exportManifest omits blank filters and falls back to a default filename', async () => {
    vi.useFakeTimers()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:fallback-url')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    let downloadedName = ''
    vi.spyOn(HTMLAnchorElement.prototype, 'download', 'set').mockImplementation(
      (value: string) => {
        downloadedName = value
      }
    )

    const spy = vi.spyOn(apiClient, 'get').mockResolvedValueOnce({
      data: new Blob(['']),
      status: 200,
      statusText: 'OK',
      headers: {},
      config: {} as AxiosResponse['config'],
    })

    await personsApi.exportManifest({ search: '   ', faceStatus: '' })

    expect(spy).toHaveBeenCalledWith('/persons/export', {
      params: { search: undefined, faceStatus: undefined },
      responseType: 'blob',
    })
    expect(downloadedName).toBe('person_photo_manifest.csv')
    vi.useRealTimers()
  })

  it('exportManifest surfaces the server message when the error body is a JSON blob', async () => {
    // `responseType: 'blob'` 下错误体也是 Blob：不显式识别就会退化成
    // "Request failed with status code 500"，用户看不到服务端的真实原因。
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce({
      data: new Blob(
        [
          JSON.stringify({
            code: 'INTERNAL_ERROR',
            message: '导出失败：磁盘写入错误',
            data: null,
          }),
        ],
        { type: 'application/json' }
      ),
      status: 500,
      statusText: 'OK',
      headers: {},
      config: {} as AxiosResponse['config'],
    })

    await expect(personsApi.exportManifest()).rejects.toThrow(
      '导出失败：磁盘写入错误'
    )
  })
})
