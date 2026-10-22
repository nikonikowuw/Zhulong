import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import {
  camerasApi,
  type CreateCameraDto,
  type GetCamerasParams,
  type UpdateCameraDto,
} from '../api/cameras-api'
import {
  type Camera,
  type CameraCredentialsResponse,
  type CameraListResponse,
  type DiagnoseResponse,
} from '../data/schema'

export const CAMERAS_QUERY_KEY = ['cameras'] as const

/**
 * 获取摄像机分页列表
 */
export function useCameras(
  params?: GetCamerasParams
): UseQueryResult<CameraListResponse, Error> {
  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, params],
    queryFn: () => camerasApi.getCameras(params),
    placeholderData: (previousData) => previousData,
  })
}

/**
 * 获取全量摄像机列表（不分页）
 *
 * 专为需要完整设备树的页面（如实时预览侧栏）设计。
 * queryKey 以 `CAMERAS_QUERY_KEY` 为前缀，因此 `useCameraEvents` 的 SSE 增量
 * 会通过 `setQueriesData` 自动 patch 到这里，无需轮询。
 */
export function useAllCameras(): UseQueryResult<CameraListResponse, Error> {
  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, 'all'],
    queryFn: () => camerasApi.getAll(),
    // SSE 是主通道；窗口重新聚焦作为兼底，不做定时轮询
    staleTime: 60_000,
    refetchInterval: false,
  })
}

/**
 * 获取单台摄像机详情
 */
export function useCamera(id?: string): UseQueryResult<Camera, Error> {
  return useQuery({
    queryKey: [...CAMERAS_QUERY_KEY, 'detail', id],
    queryFn: () => (id ? camerasApi.getCamera(id) : Promise.reject('No id')),
    enabled: Boolean(id),
  })
}

/**
 * 新增摄像机 Mutation
 */
export function useCreateCamera() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: CreateCameraDto) => camerasApi.createCamera(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
    },
  })
}

/**
 * 编辑摄像机 Mutation
 */
export function useUpdateCamera() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateCameraDto }) =>
      camerasApi.updateCamera(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
    },
  })
}

/**
 * 快捷启停摄像机 Mutation
 */
export function useToggleCameraEnabled(options?: {
  suppressGlobalToast?: boolean
}) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ camera, enabled }: { camera: Camera; enabled: boolean }) =>
      camerasApi.updateCamera(camera.id, {
        revision: camera.revision,
        enabled,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
    },
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

/**
 * 删除摄像机 Mutation
 */
export function useDeleteCamera(options?: { suppressGlobalToast?: boolean }) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => camerasApi.deleteCamera(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
    },
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}

/**
 * 获取明文凭据 Query
 */
export function useCameraCredentials(id: string | null, enabled = false) {
  return useQuery<CameraCredentialsResponse, Error>({
    queryKey: [...CAMERAS_QUERY_KEY, 'credentials', id],
    queryFn: () =>
      id ? camerasApi.getCredentials(id) : Promise.reject('No ID'),
    enabled: Boolean(id) && enabled,
    staleTime: 0,
    gcTime: 0, // 不在本地缓存长效保存明文密码
  })
}

/**
 * 手动主动诊断 Mutation
 */
export function useDiagnoseCamera(options?: { suppressGlobalToast?: boolean }) {
  return useMutation<DiagnoseResponse, Error, string>({
    mutationFn: (id: string) => camerasApi.diagnose(id),
    meta: {
      suppressGlobalToast: options?.suppressGlobalToast,
    },
  })
}
