import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import {
  tasksApi,
  type CreateTaskDto,
  type GetTasksParams,
  type UpdateTaskDto,
} from '../api/tasks-api'
import { type Task } from '../data/schema'

export const TASKS_QUERY_KEY = ['tasks'] as const

/**
 * 获取分析任务列表
 */
export function useTasks(
  params?: GetTasksParams
): UseQueryResult<Task[], Error> {
  return useQuery({
    queryKey: [...TASKS_QUERY_KEY, params],
    queryFn: () => tasksApi.getTasks(params),
    staleTime: 5000,
  })
}

/**
 * 获取指定分析任务详情
 */
export function useTask(id?: string): UseQueryResult<Task, Error> {
  return useQuery({
    queryKey: [...TASKS_QUERY_KEY, 'detail', id],
    queryFn: () => (id ? tasksApi.getTask(id) : Promise.reject('No id')),
    enabled: Boolean(id),
  })
}

/**
 * 创建分析任务 Mutation
 */
export function useCreateTask() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: CreateTaskDto) => tasksApi.createTask(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}

/**
 * 更新分析任务 Mutation
 */
export function useUpdateTask() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateTaskDto }) =>
      tasksApi.updateTask(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}

/**
 * 删除分析任务 Mutation
 */
export function useDeleteTask() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => tasksApi.deleteTask(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: TASKS_QUERY_KEY })
    },
  })
}
