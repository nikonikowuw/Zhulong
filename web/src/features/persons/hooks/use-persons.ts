import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import {
  personsApi,
  type GetPersonsParams,
} from '../api/persons-api'
import {
  type CreatePersonDto,
  type FaceListResponse,
  type Person,
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
 * 获取单个人员详情
 */
export function usePerson(id?: string): UseQueryResult<Person, Error> {
  return useQuery({
    queryKey: [...PERSONS_QUERY_KEY, 'detail', id],
    queryFn: () => (id ? personsApi.getPerson(id) : Promise.reject('No id')),
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
        : Promise.reject('No personId'),
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
export function useDeletePerson() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => personsApi.deletePerson(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
  })
}

/**
 * 录入人脸 Mutation（图片文件直传）
 */
export function useAddFace() {
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
      void queryClient.invalidateQueries({
        queryKey: [...PERSONS_QUERY_KEY, variables.personId, 'faces'],
      })
      void queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })
    },
  })
}

/**
 * 删除单张人脸 Mutation
 */
export function useDeleteFace() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      personId,
      faceId,
    }: {
      personId: string
      faceId: string
    }) => personsApi.deleteFace(personId, faceId),
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
    mutationFn: ({
      personId,
      faceId,
    }: {
      personId: string
      faceId: string
    }) => personsApi.setPrimaryFace(personId, faceId),
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
