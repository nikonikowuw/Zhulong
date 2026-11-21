import { z } from 'zod'
import { apiClient } from '@/lib/api-client'

const healthPayloadSchema = z.object({
  status: z.literal('ready'),
  components: z.record(z.string(), z.string()).optional(),
  decode: z
    .object({
      hardware_available: z.boolean().optional(),
    })
    .optional(),
})

const healthResponseSchema = z.object({
  code: z.literal('OK'),
  message: z.string(),
  data: healthPayloadSchema,
})

/** 登录页页脚展示的服务就绪状态。 */
export type SystemHealthState =
  /** 数据库与引擎都就绪 */
  | 'ready'
  /** 能连上服务，但某个组件降级（如解码后端不可用） */
  | 'degraded'
  /** 服务已响应但尚未就绪（/health 503） */
  | 'starting'
  /** 后端不可达（进程没起、代理 502、网络断开） */
  | 'unavailable'

export interface SystemHealth {
  state: SystemHealthState
  /**
   * 硬件解码此刻是否真的可用。页脚据此决定写「硬件解码」还是「软件解码」——
   * 旧版无条件写死「NPU 加速活跃」，在纯 CPU 机器上是假话。
   * 未知（老版本后端没带 decode 字段）时为 undefined。
   */
  hardwareDecode?: boolean
}

/**
 * `/api/v1/health` 的 data 载荷（见 internal/app/router.go 的 HealthData / DecodeStatus）。
 * 字段名是 snake_case 的 wire 格式，不要按 Go 结构体名猜。
 */
export type HealthPayload = z.infer<typeof healthPayloadSchema>

/**
 * 把 health 载荷映射成页脚状态。抽成纯函数是为了能用真实报文做单测——
 * 这里的字段名一旦与后端契约脱节，页脚会静默地永远显示错误状态。
 */
function toSystemHealth(payload: HealthPayload): SystemHealth {
  const components = payload.components ?? {}
  // 缺失的组件视为「未知」而非「降级」：logging 在 LogSink 未注入时本就不上报。
  const isDegraded = Object.values(components).some(
    (state) => state !== 'ready'
  )
  return {
    state: isDegraded ? 'degraded' : 'ready',
    hardwareDecode: payload.decode?.hardware_available,
  }
}

/** 解析 data 载荷；未知或不完整的状态一律不能被误报为 ready。 */
export function mapHealthPayload(payload: unknown): SystemHealth {
  const parsed = healthPayloadSchema.safeParse(payload)
  if (!parsed.success) {
    return { state: 'starting' }
  }
  return toSystemHealth(parsed.data)
}

/** 校验完整 API 信封及健康载荷，避免 Axios 泛型将网络 JSON 当作已验证数据。 */
export function mapHealthResponse(response: unknown): SystemHealth {
  const parsed = healthResponseSchema.safeParse(response)
  if (!parsed.success) {
    return { state: 'starting' }
  }
  return toSystemHealth(parsed.data.data)
}

export const healthApi = {
  /**
   * 探测宿主就绪状态，供登录页页脚显示真实服务状态。
   *
   * `validateStatus` 一律放行：503 是「还没就绪」的业务状态，不是异常，
   * 让它走 reject 分支会把「启动中」和「连不上」混成同一条文案。
   */
  async get(): Promise<SystemHealth> {
    try {
      const res = await apiClient.get<unknown>('/health', {
        validateStatus: () => true,
      })
      if (res.status !== 200) {
        return { state: 'starting' }
      }
      return mapHealthResponse(res.data)
    } catch {
      return { state: 'unavailable' }
    }
  },
}
