import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { personsApi, type GetPersonsParams } from '../api/persons-api'
import {
  PERSONS_MAX_PAGE_SIZE,
  type CreatePersonDto,
  type FaceImportJobStatus,
  type FaceListResponse,
  type Person,
  type PersonImportCommitResponse,
  type PersonImportPreviewResponse,
  type PersonListResponse,
  type UpdatePersonDto,
} from '../data/schema'
import { mapWithConcurrency } from '../persons-helpers'

export const PERSONS_QUERY_KEY = ['persons'] as const

/**
 * 异步后台任务（ZIP 批量导入 / 特征重提）的独立 query key 前缀。
 *
 * 与 `PERSONS_QUERY_KEY` 隔离：刷新人员列表/详情时不会连带把已完成任务或
 * `latest` 状态轮询也一并 invalidate 引发冗余请求。
 */
const PERSON_JOBS_QUERY_KEY = ['persons-jobs'] as const

function faceImportJobQueryKey(jobId: string | null) {
  return [...PERSON_JOBS_QUERY_KEY, 'face-import', jobId] as const
}

function faceReextractJobQueryKey(jobId: string | null) {
  return [...PERSON_JOBS_QUERY_KEY, 'face-reextract', jobId] as const
}

export const LATEST_FACE_REEXTRACT_JOB_QUERY_KEY = [
  ...PERSON_JOBS_QUERY_KEY,
  'face-reextract',
  'latest',
] as const

/** 单个人脸底库的 query key。失效时按 personId 精确命中。 */
export function personFacesQueryKey(personId: string) {
  return [...PERSONS_QUERY_KEY, personId, 'faces'] as const
}

/**
 * 把可选参数归一化成**固定字段**的查询键。
 *
 * 调用方每次渲染都会新建一个对象字面量，`{a:1}` 与 `{a:1,b:undefined}` 在
 * TanStack Query 的结构哈希里是两个不同的 key。值一样就不应该重新请求，
 * 因此这里只给有值的字段留位。
 */
function personsQueryKey(params?: GetPersonsParams) {
  return [
    ...PERSONS_QUERY_KEY,
    params?.page ?? 1,
    params?.pageSize ?? 10,
    params?.search ?? '',
    params?.faceStatus ?? '',
  ] as const
}

/**
 * 获取人员分页列表
 *
 * `placeholderData` 让翻页时保留上一页内容，避免整表闪成骨架屏；同时把
 * `isPlaceholderData` 透出去，调用方可以据此做「正在载入下一页」的弱提示。
 */
export function usePersons(
  params?: GetPersonsParams
): UseQueryResult<PersonListResponse, Error> {
  return useQuery({
    queryKey: personsQueryKey(params),
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
 *
 * `id` 为空时返回 `null` 而不是 `Promise.reject`：拒绝态会污染全局 QueryCache
 * 的 401/500 分支，也会让调用方拿到一个假的 Error。
 */
export function usePerson(id?: string): UseQueryResult<Person | null, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, 'detail', id],
    queryFn: () => (id ? personsApi.getPerson(id) : null),
    enabled: Boolean(id),
  })
}

/**
 * 获取指定人员关联的人脸底库列表（分页信封，total 为后端真实总数）
 *
 * `pageSize` 取后端硬上限：一个人最多 `MaxFacesPerPerson`（10）张底库，
 * 上限 100 足够覆盖，也不会有翻页语义。
 */
export function usePersonFaces(
  personId?: string
): UseQueryResult<FaceListResponse | null, Error> {
  return useQuery({
    queryKey: personFacesQueryKey(personId ?? ''),
    queryFn: () =>
      personId
        ? personsApi.listFaces(personId, { pageSize: PERSONS_MAX_PAGE_SIZE })
        : null,
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
    meta: { suppressGlobalToast: true },
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
    meta: { suppressGlobalToast: true },
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
          queryKey: personFacesQueryKey(variables.personId),
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
export function useDeleteFace(options?: { suppressGlobalToast?: boolean }) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ personId, faceId }: { personId: string; faceId: string }) =>
      personsApi.deleteFace(personId, faceId),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: personFacesQueryKey(variables.personId),
      })
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
    meta: { suppressGlobalToast: options?.suppressGlobalToast },
  })
}

/**
 * 设置主人脸 Mutation
 */
export function useSetPrimaryFace(options?: { suppressGlobalToast?: boolean }) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ personId, faceId }: { personId: string; faceId: string }) =>
      personsApi.setPrimaryFace(personId, faceId),
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
      void queryClient.invalidateQueries({
        queryKey: personFacesQueryKey(variables.personId),
      })
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, 'detail', variables.personId],
      })
    },
    meta: { suppressGlobalToast: options?.suppressGlobalToast },
  })
}

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
        pageSize: PERSONS_MAX_PAGE_SIZE,
      })
      if (first.items.length >= first.total) {
        return first
      }

      const pageCount = Math.ceil(first.total / PERSONS_MAX_PAGE_SIZE)
      const remainingPages = Array.from(
        { length: pageCount - 1 },
        (_, i) => i + 2
      )
      const restResults = await mapWithConcurrency(remainingPages, 4, (page) =>
        personsApi.getPersons({
          page,
          pageSize: PERSONS_MAX_PAGE_SIZE,
        })
      )

      const items = [...first.items]
      for (const res of restResults) {
        if (res.status === 'fulfilled') {
          items.push(...res.value.items)
        } else {
          throw res.reason
        }
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
    meta: { suppressGlobalToast: true },
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
    meta: { suppressGlobalToast: true },
  })
}

/** 人脸批量导入任务的终态集合。用 `Set` 而非数组：不需要逐个 `as` 断言。 */
const FACE_IMPORT_TERMINAL_STATUSES = new Set<FaceImportJobStatus>([
  'COMPLETED',
  'FAILED',
  'CANCELLED',
])

/**
 * 轮询/查询人脸批量导入任务状态
 *
 * 轮询间隔随进度收敛：刚开始 1s 一次，任务进入中后段后 2s 一次即可，
 * 完成后立刻停表。
 */
export function useFaceImportJob(
  jobId: string | null,
  enabled: boolean,
  options?: { poll?: boolean }
) {
  const poll = options?.poll ?? true
  return useQuery({
    queryKey: faceImportJobQueryKey(jobId),
    queryFn: () => (jobId ? personsApi.getFaceImportJob(jobId) : null),
    enabled: Boolean(jobId && enabled),
    refetchInterval: poll
      ? (query) => {
          const data = query.state.data
          if (!data) return 1000
          if (FACE_IMPORT_TERMINAL_STATUSES.has(data.status)) return false
          return data.processed >= data.total / 2 ? 2000 : 1000
        }
      : false,
  })
}

/**
 * 创建人脸特征重新提取任务
 */
export function useCreateFaceReextractJob() {
  return useMutation({
    mutationFn: (personIds?: string[]) =>
      personsApi.createFaceReextractJob(personIds),
    meta: { suppressGlobalToast: true },
  })
}

/**
 * 查询最近一次人脸特征重新提取任务（用于在页面刷新后恢复后台任务跟踪）
 */
export function useLatestFaceReextractJob(enabled = true) {
  return useQuery({
    queryKey: LATEST_FACE_REEXTRACT_JOB_QUERY_KEY,
    queryFn: () => {
      if (typeof personsApi?.getLatestFaceReextractJob !== 'function') {
        return null
      }
      return personsApi.getLatestFaceReextractJob()
    },
    enabled,
    staleTime: 10_000,
  })
}

/**
 * 轮询/查询人脸特征重新提取任务状态
 */
export function useFaceReextractJob(
  jobId: string | null,
  enabled: boolean,
  options?: { poll?: boolean }
) {
  const poll = options?.poll ?? true
  return useQuery({
    queryKey: faceReextractJobQueryKey(jobId),
    queryFn: () => {
      if (!jobId || typeof personsApi?.getFaceReextractJob !== 'function') {
        return null
      }
      return personsApi.getFaceReextractJob(jobId)
    },
    enabled: Boolean(jobId && enabled),
    refetchInterval: poll
      ? (query) => {
          const data = query.state.data
          if (!data) return 1000
          if (FACE_IMPORT_TERMINAL_STATUSES.has(data.status)) return false
          return data.processed >= data.total / 2 ? 2000 : 1000
        }
      : false,
  })
}

/**
 * 取消人脸特征重新提取任务
 */
export function useCancelFaceReextractJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobId: string) => personsApi.cancelFaceReextractJob(jobId),
    onSuccess: (_data, jobId) => {
      void queryClient.invalidateQueries({
        queryKey: faceReextractJobQueryKey(jobId),
      })
      void queryClient.invalidateQueries({
        queryKey: LATEST_FACE_REEXTRACT_JOB_QUERY_KEY,
      })
    },
    meta: { suppressGlobalToast: true },
  })
}
