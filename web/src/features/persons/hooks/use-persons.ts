import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { personsApi, type GetPersonsParams } from '../api/persons-api'
import {
  type CreatePersonDto,
  type FaceListResponse,
  type Person,
  type PersonImportCommitResponse,
  type PersonImportPreviewResponse,
  type PersonListResponse,
  type UpdatePersonDto,
} from '../data/schema'

export const PERSONS_QUERY_KEY = ['persons'] as const

/**
 * 获取人员分页列表
 */
export function usePersons(
  params?: GetPersonsParams
): UseQueryResult<PersonListResponse, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, params],
    queryFn: () => personsApi.getPersons(params),
    placeholderData: (previousData) => previousData,
  })
}

/**
 * 预览人员 CSV 导入。
 */
export function usePreviewPersonImport() {
  return useMutation<PersonImportPreviewResponse, Error, File>({
    mutationFn: (file) => personsApi.previewImport(file),
    meta: { suppressGlobalToast: true },
  })
}

/**
 * 确认人员 CSV 导入并刷新人员列表。
 */
export function useCommitPersonImport() {
  const queryClient = useQueryClient()

  return useMutation<PersonImportCommitResponse, Error, File>({
    mutationFn: (file) => personsApi.commitImport(file),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
    meta: { suppressGlobalToast: true },
  })
}

/**
 * 获取单个人员详情
 */
export function usePerson(id?: string): UseQueryResult<Person, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, 'detail', id],
    queryFn: () =>
      id ? personsApi.getPerson(id) : Promise.reject(new Error('No id')),
    enabled: Boolean(id),
  })
}

/**
 * 获取指定人员关联的人脸底库列表（分页信封，total 为后端真实总数）
 */
export function usePersonFaces(
  personId?: string
): UseQueryResult<FaceListResponse, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, personId, 'faces'],
    queryFn: () =>
      personId
        ? personsApi.listFaces(personId, { pageSize: 100 })
        : Promise.reject(new Error('No personId')),
    enabled: Boolean(personId),
  })
}

/**
 * 创建人员 Mutation
 */
export function useCreatePerson() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: CreatePersonDto) => personsApi.createPerson(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
  })
}

/**
 * 更新人员 Mutation
 */
export function useUpdatePerson() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdatePersonDto }) =>
      personsApi.updatePerson(id, data),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, 'detail', variables.id],
      })
    },
  })
}

/**
 * 删除人员 Mutation
 */
export function useDeletePerson(options?: {
  suppressGlobalToast?: boolean
  suppressInvalidate?: boolean
}) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => personsApi.deletePerson(id),
    onSuccess: () => {
      if (!options?.suppressInvalidate) {
        void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      }
    },
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

/**
 * 录入人脸 Mutation（图片文件直传）
 */
export function useAddFace(options?: {
  suppressGlobalToast?: boolean
  suppressInvalidate?: boolean
}) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      personId,
      data,
    }: {
      personId: string
      data: File | FormData
    }) => personsApi.addFace(personId, data),
    onSuccess: (_data, variables) => {
      if (!options?.suppressInvalidate) {
        void queryClient.invalidateQueries({
          queryKey: [...PERSONS_QUERY_KEY, variables.personId, 'faces'],
        })
        void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      }
    },
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

/**
 * 删除单张人脸 Mutation
 */
export function useDeleteFace() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ personId, faceId }: { personId: string; faceId: string }) =>
      personsApi.deleteFace(personId, faceId),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, variables.personId, 'faces'],
      })
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
  })
}

/**
 * 设置主人脸 Mutation
 */
export function useSetPrimaryFace() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ personId, faceId }: { personId: string; faceId: string }) =>
      personsApi.setPrimaryFace(personId, faceId),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, variables.personId, 'faces'],
      })
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, 'detail', variables.personId],
      })
    },
  })
}

/** 后端分页硬上限（`httputil.MaxPageSize`）。 */
const PERSONS_FETCH_PAGE_SIZE = 100

/**
 * 获取全量人员列表（内部按分页上限逐页拉全）。
 *
 * 人员选择器需要的是完整候选集：直接传 `pageSize: 100` 会在超过上限时静默
 * 丢掉其余人员，且与后端 `MaxPageSize` 一改就悄悄失真。
 */
export function useAllPersons(): UseQueryResult<PersonListResponse, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, 'all'],
    queryFn: async () => {
      const first = await personsApi.getPersons({
        page: 1,
        pageSize: PERSONS_FETCH_PAGE_SIZE,
      })
      if (first.items.length >= first.total) {
        return first
      }

      const pageCount = Math.ceil(first.total / PERSONS_FETCH_PAGE_SIZE)
      const remainingPages = Array.from(
        { length: pageCount - 1 },
        (_, i) => i + 2
      )
      const restResults = await Promise.all(
        remainingPages.map((page) =>
          personsApi.getPersons({
            page,
            pageSize: PERSONS_FETCH_PAGE_SIZE,
          })
        )
      )

      const items = [...first.items]
      for (const res of restResults) {
        items.push(...res.items)
      }

      return { ...first, items, total: first.total }
    },
    staleTime: 60_000,
  })
}

/**
 * 创建人脸批量导入任务
 */
export function useCreateFaceImportJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (file: File) => personsApi.createFaceImportJob(file),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
  })
}

/**
 * 导出待采像名单（照片采像清单 CSV）
 *
 * 下载是一次性命令，不缓存任何服务端状态，因此只借 mutation 拿 isPending。
 */
export function useExportPersonsManifest() {
  return useMutation({
    mutationFn: (params: { search?: string; faceStatus?: string }) =>
      personsApi.exportManifest(params),
  })
}

/**
 * 轮询/查询人脸批量导入任务状态
 */
export function useFaceImportJob(jobId: string | null, enabled: boolean) {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, 'face-import-job', jobId],
    queryFn: () => (jobId ? personsApi.getFaceImportJob(jobId) : null),
    enabled: Boolean(jobId && enabled),
    refetchInterval: (query) => {
      const data = query.state.data
      if (!data) return 1000
      if (
        data.status === 'COMPLETED' ||
        data.status === 'FAILED' ||
        data.status === 'CANCELLED'
      ) {
        return false
      }
      return 1000
    },
  })
}
