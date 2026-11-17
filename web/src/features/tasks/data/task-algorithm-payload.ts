import { type Task } from './schema'

/**
 * 把整条通道的算法订阅拍成 `UpdateTaskRequest.algorithms` 载荷。
 *
 * 后端 update 对 `algorithms` 是**整体替换**语义，因此必须回传全部子项；
 * `override` 只用于就地翻转单个订阅项的开关。
 */
export function taskAlgorithmsPayload(
  task: Task,
  override?: { id: string; enabled: boolean }
) {
  return (task.algorithms ?? []).map((algo) => ({
    id: algo.id,
    algorithmId: algo.algorithmId,
    sampleFps: algo.sampleFps,
    enabled:
      override && algo.id === override.id ? override.enabled : algo.enabled,
    configJson: algo.configJson,
    regionConfigJson: algo.regionConfigJson,
    // 整体替换语义：漏传会把已开启的录像配置清回默认（关闭）。
    clipConfigJson: algo.clipConfigJson || '{}',
    scheduleJson: algo.scheduleJson || '{}',
  }))
}
