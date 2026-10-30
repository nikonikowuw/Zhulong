import { useMutation } from '@tanstack/react-query'
import { spatialApi } from '../api/spatial-api'
import { type TrajectoryQueryParams } from '../data/schema'

/**
 * 轨迹检索是「带着参数取一次结果」的即时查询，不是可缓存的列表，
 * 因此用 mutation 承载而不是 query。
 */
export function useTrajectory() {
  return useMutation({
    mutationFn: (params: TrajectoryQueryParams) =>
      spatialApi.queryTrajectory(params),
  })
}
