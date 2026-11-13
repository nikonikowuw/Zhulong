import {
  MULTIPART_HEADERS,
  apiClient,
  type ApiResponse,
} from '@/lib/api-client'
import { triggerDownload } from '@/lib/csv'
import { getGalleryImageUrl } from '@/lib/media-url'
import {
  cancelFaceReextractJobResponseSchema,
  faceImportJobResponseSchema,
  faceListResponseSchema,
  faceReextractJobResponseSchema,
  faceSchema,
  personImportCommitResponseSchema,
  personImportPreviewResponseSchema,
  personListResponseSchema,
  personSchema,
  setPrimaryFaceSchema,
  type Face,
  type FaceImportJobResponse,
  type FaceListResponse,
  type FaceReextractJobResponse,
  type Person,
  type PersonImportCommitResponse,
  type PersonImportPreviewResponse,
  type PersonListResponse,
  type CreatePersonDto,
  type UpdatePersonDto,
} from '../data/schema'

export interface GetPersonsParams {
  page?: number
  pageSize?: number
  search?: string
  /** 单个人脸底库状态（`enrolled` / `missing`），后端只接受单值。 */
  faceStatus?: string
}

interface GetFacesParams {
  page?: number
  pageSize?: number
}

/**
 * 从 Content-Disposition 解析下载文件名。
 *
 * 优先 `filename*=UTF-8''…`（RFC 5987，中文名走这条），回落到 `filename="…"`。
 */
function parseContentDispositionFilename(
  disposition: string | undefined,
  fallback: string
): string {
  if (!disposition) return fallback
  const utf8Match = /filename\*=UTF-8''([^;]+)/i.exec(disposition)
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1])
    } catch {
      return fallback
    }
  }
  const match = /filename=["']?([^"';]+)["']?/.exec(disposition)
  return match?.[1] ? match[1] : fallback
}

export const personsApi = {
  /**
   * 获取人员分页列表
   */
  async getPersons(params?: GetPersonsParams): Promise<PersonListResponse> {
    const page = params?.page ?? 1
    const pageSize = params?.pageSize ?? 10
    const resp = await apiClient.get<ApiResponse<PersonListResponse>>(
      '/persons',
      {
        params: {
          page,
          pageSize,
          search: params?.search,
          faceStatus: params?.faceStatus,
        },
      }
    )
    const raw = resp.data.data ?? { items: [], total: 0, page, pageSize }
    return personListResponseSchema.parse(raw)
  },

  /**
   * 预览人员 CSV 导入，不写入人员档案
   */
  async previewImport(file: File): Promise<PersonImportPreviewResponse> {
    const formData = new FormData()
    formData.append('file', file)
    const resp = await apiClient.post<ApiResponse<PersonImportPreviewResponse>>(
      '/persons/import/preview',
      formData,
      { headers: MULTIPART_HEADERS }
    )
    return personImportPreviewResponseSchema.parse(resp.data.data)
  },

  /**
   * 确认人员 CSV 导入，服务端会重新解析上传文件
   */
  async commitImport(file: File): Promise<PersonImportCommitResponse> {
    const formData = new FormData()
    formData.append('file', file)
    const resp = await apiClient.post<ApiResponse<PersonImportCommitResponse>>(
      '/persons/import/commit',
      formData,
      { headers: MULTIPART_HEADERS }
    )
    return personImportCommitResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取单个人员详情
   */
  async getPerson(id: string): Promise<Person> {
    const resp = await apiClient.get<ApiResponse<Person>>(
      `/persons/${encodeURIComponent(id)}`
    )
    return personSchema.parse(resp.data.data)
  },

  /**
   * 创建人员档案
   */
  async createPerson(data: CreatePersonDto): Promise<Person> {
    const resp = await apiClient.post<ApiResponse<Person>>('/persons', data)
    return personSchema.parse(resp.data.data)
  },

  /**
   * 更新人员基本资料
   */
  async updatePerson(id: string, data: UpdatePersonDto): Promise<Person> {
    const resp = await apiClient.put<ApiResponse<Person>>(
      `/persons/${encodeURIComponent(id)}`,
      data
    )
    return personSchema.parse(resp.data.data)
  },

  /**
   * 删除人员档案
   */
  async deletePerson(id: string): Promise<void> {
    await apiClient.delete<ApiResponse<{ deleted: boolean }>>(
      `/persons/${encodeURIComponent(id)}`
    )
  },

  /**
   * 获取人员关联的人脸底库分页列表
   */
  async listFaces(
    personId: string,
    params?: GetFacesParams
  ): Promise<FaceListResponse> {
    const page = params?.page ?? 1
    const pageSize = params?.pageSize ?? 20
    const resp = await apiClient.get<ApiResponse<FaceListResponse>>(
      `/persons/${encodeURIComponent(personId)}/faces`,
      { params: { page, pageSize } }
    )
    const raw = resp.data.data ?? { items: [], total: 0, page, pageSize }
    return faceListResponseSchema.parse(raw)
  },

  /**
   * 录入新人脸底库（本地图片文件直传）。
   *
   * 必须显式传 `MULTIPART_HEADERS`：不传会继承客户端默认的
   * `application/json`，Axios 在 `transformRequest` 阶段把 FormData 序列化成
   * `{"file":{}}`，文件字节整段丢失，后端 `c.FormFile` 报 422。
   */
  async addFace(personId: string, data: File | FormData): Promise<Face> {
    const formData = data instanceof FormData ? data : new FormData()
    if (data instanceof File) {
      formData.append('file', data)
    }

    const resp = await apiClient.post<ApiResponse<Face>>(
      `/persons/${encodeURIComponent(personId)}/faces`,
      formData,
      { headers: MULTIPART_HEADERS }
    )
    return faceSchema.parse(resp.data.data)
  },

  /**
   * 获取指定人脸底库图片的只读访问 URL。
   *
   * 统一委托给 `@/lib/media-url` 的 `getGalleryImageUrl`：那里做了
   * `encodeURIComponent`，且是全站唯一的底库图片 URL 拼接点。曾经这里、API 层和
   * image-search 各写一份，其中两份忘了转义，ID 含空格/斜杠就 404。
   */
  getFaceImageUrl(personId: string, faceId: string): string {
    return getGalleryImageUrl(personId, faceId)
  },

  /**
   * 删除单个人脸底库
   */
  async deleteFace(personId: string, faceId: string): Promise<void> {
    await apiClient.delete<ApiResponse<{ deleted: boolean }>>(
      `/persons/${encodeURIComponent(personId)}/faces/${encodeURIComponent(faceId)}`
    )
  },

  /**
   * 设置指定人脸为主人脸
   */
  async setPrimaryFace(personId: string, faceId: string): Promise<Person> {
    const payload = setPrimaryFaceSchema.parse({ faceId })
    const resp = await apiClient.put<ApiResponse<Person>>(
      `/persons/${encodeURIComponent(personId)}/primary-face`,
      payload
    )
    return personSchema.parse(resp.data.data)
  },

  /**
   * 创建人脸批量导入异步任务
   */
  async createFaceImportJob(file: File): Promise<FaceImportJobResponse> {
    const formData = new FormData()
    formData.append('file', file)
    const resp = await apiClient.post<ApiResponse<FaceImportJobResponse>>(
      '/persons/import-face-jobs',
      formData,
      { headers: MULTIPART_HEADERS }
    )
    return faceImportJobResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取人脸批量导入任务进度状态
   */
  async getFaceImportJob(jobId: string): Promise<FaceImportJobResponse> {
    const resp = await apiClient.get<ApiResponse<FaceImportJobResponse>>(
      `/persons/import-face-jobs/${encodeURIComponent(jobId)}`
    )
    return faceImportJobResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取失败报告 CSV 下载地址。相对路径，交给浏览器按当前 origin 解析。
   */
  getFaceImportFailedReportUrl(jobId: string): string {
    return `/api/v1/persons/import-face-jobs/${encodeURIComponent(jobId)}/failed-report`
  },

  /**
   * 创建人脸特征重新提取异步任务（不传 personIds 或为空数组则重提全库）
   */
  async createFaceReextractJob(
    personIds?: string[]
  ): Promise<FaceReextractJobResponse> {
    const payload = personIds && personIds.length > 0 ? { personIds } : {}
    const resp = await apiClient.post<ApiResponse<FaceReextractJobResponse>>(
      '/persons/reextract-face-jobs',
      payload
    )
    return faceReextractJobResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取最近一次人脸特征重新提取任务（无记录时返回 null）
   */
  async getLatestFaceReextractJob(): Promise<FaceReextractJobResponse | null> {
    const resp = await apiClient.get<
      ApiResponse<FaceReextractJobResponse | null>
    >('/persons/reextract-face-jobs/latest')
    if (!resp.data.data) return null
    return faceReextractJobResponseSchema.parse(resp.data.data)
  },

  /**
   * 获取人脸特征重新提取任务进度状态
   */
  async getFaceReextractJob(jobId: string): Promise<FaceReextractJobResponse> {
    const resp = await apiClient.get<ApiResponse<FaceReextractJobResponse>>(
      `/persons/reextract-face-jobs/${encodeURIComponent(jobId)}`
    )
    return faceReextractJobResponseSchema.parse(resp.data.data)
  },

  /**
   * 取消正在执行或排队中的人脸特征重新提取任务
   */
  async cancelFaceReextractJob(jobId: string): Promise<boolean> {
    const resp = await apiClient.post<ApiResponse<{ cancelled: boolean }>>(
      `/persons/reextract-face-jobs/${encodeURIComponent(jobId)}/cancel`
    )
    return cancelFaceReextractJobResponseSchema.parse(resp.data.data).cancelled
  },

  /**
   * 获取特征重提失败报告 CSV 下载地址
   */
  getFaceReextractFailedReportUrl(jobId: string): string {
    return `/api/v1/persons/reextract-face-jobs/${encodeURIComponent(jobId)}/failed-report`
  },

  /**
   * 导出人员照片采像清单 CSV（支持按关键词与底库状态过滤）。
   *
   * 错误处理不在本函数：`responseType: 'blob'` 下服务端错误体也是 Blob，
   * `@/lib/api-client` 的响应拦截器会把它读回文本并把服务端消息写到
   * `error.message`。这里只负责 2xx 的成功路径。
   */
  async exportManifest(params?: {
    search?: string
    faceStatus?: string
  }): Promise<void> {
    const resp = await apiClient.get<Blob>('/persons/export', {
      params: {
        search: params?.search?.trim() || undefined,
        faceStatus: params?.faceStatus?.trim() || undefined,
      },
      responseType: 'blob',
    })

    const filename = parseContentDispositionFilename(
      resp.headers['content-disposition'] as string | undefined,
      'person_photo_manifest.csv'
    )
    triggerDownload(resp.data, filename)
  },
}
