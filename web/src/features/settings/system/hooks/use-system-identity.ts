import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { systemApi } from '../api/system-api'

export const SYSTEM_IDENTITY_QUERY_KEY = ['system', 'identity']

export function useSystemIdentity() {
  return useQuery({
    queryKey: SYSTEM_IDENTITY_QUERY_KEY,
    queryFn: () => systemApi.getIdentity(),
    // 设备标识只有人工修改才会变，没必要跟着 2.5s 的遥测轮询一起刷。
    staleTime: 60_000,
  })
}

export function useUpdateSystemIdentity() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (deviceId: string) => systemApi.updateIdentity(deviceId),
    onSuccess: (identity) => {
      queryClient.setQueryData(SYSTEM_IDENTITY_QUERY_KEY, identity)
    },
  })
}
