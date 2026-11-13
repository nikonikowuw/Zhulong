import { z } from 'zod'
import { type TFunction } from 'i18next'
import type { AuthType } from '../types'

export const RESERVED_HEADERS = [
  'authorization',
  'content-type',
  'content-length',
  'user-agent',
  'host',
  'accept',
  'connection',
] as const

export interface HeaderEntry {
  key: string
  value: string
}

export function areHeadersUntouched(
  headers: HeaderEntry[],
  originalHeaderNames: string[]
): boolean {
  if (headers.length !== originalHeaderNames.length) {
    return false
  }
  return headers.every(
    (h, index) => h.key.trim() === originalHeaderNames[index] && h.value === ''
  )
}

function isValidHttpUrlWithoutUserinfo(raw: string): boolean {
  try {
    const parsed = new URL(raw)
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      !parsed.username &&
      !parsed.password &&
      Boolean(parsed.hostname)
    )
  } catch {
    return false
  }
}

export function isSameHttpOrigin(rawA: string, rawB: string): boolean {
  try {
    const a = new URL(rawA.trim())
    const b = new URL(rawB.trim())
    if (
      (a.protocol !== 'http:' && a.protocol !== 'https:') ||
      a.protocol !== b.protocol
    ) {
      return false
    }
    if (a.hostname.toLowerCase() !== b.hostname.toLowerCase()) {
      return false
    }
    const portA = a.port || (a.protocol === 'https:' ? '443' : '80')
    const portB = b.port || (b.protocol === 'https:' ? '443' : '80')
    return portA === portB
  } catch {
    return false
  }
}

/**
 * 告警推送目标表单校验 Schema 工厂
 */
export function createAlertDestinationFormSchema(
  t?: TFunction,
  isEdit = false,
  secretRetained = false,
  originalAuthType?: AuthType,
  originalHeaderNames: string[] = []
) {
  const msg = {
    nameRequired: t ? t('validation.nameRequired') : '请输入通道名称',
    nameMax: t ? t('validation.nameMax') : '通道名称不能超过 64 个字符',
    capabilityRequired: t
      ? t('validation.capabilityRequired')
      : '请至少开启“感知事件推送”或“设备存活上报”中的一项能力',
    heartbeatIntervalRange: t
      ? t('validation.heartbeatIntervalRange')
      : '上报间隔必须在 15 到 3600 秒之间',
    timeoutRange: t
      ? t('validation.timeoutRange')
      : '超时时间必须在 1 到 30 秒之间',
    keepAliveRange: t
      ? t('validation.keepAliveRange')
      : '保活间隔必须在 5 到 300 秒之间',
    heartbeatCrossOrigin: t
      ? t('validation.heartbeatCrossOrigin')
      : '配置了鉴权凭据或自定义标头时，独立存活 URL 必须与主 Webhook URL 同源',
    urlRequired: t ? t('validation.urlRequired') : '请输入 Webhook URL',
    urlInvalid: t
      ? t('validation.urlInvalid')
      : 'URL 必须以 http:// 或 https:// 开头且不能包含账号凭据',
    tokenRequired: t ? t('validation.tokenRequired') : 'Bearer Token 不能为空',
    basicUserRequired: t
      ? t('validation.basicUserRequired')
      : 'Basic 用户名不能为空',
    basicPassRequired: t
      ? t('validation.basicPassRequired')
      : 'Basic 密码不能为空',
    brokerRequired: t
      ? t('validation.brokerRequired')
      : '请输入 MQTT Broker 地址',
    brokerInvalid: t
      ? t('validation.brokerInvalid')
      : 'Broker 地址不能包含账号凭证（如 user:pass@）',
    topicRequired: t ? t('validation.topicRequired') : '请输入发布 Topic',
    topicNoWildcards: t
      ? t('validation.topicNoWildcards')
      : '发布主题不能包含通配符（+ 或 #）',
    clientIdMax: t
      ? t('validation.clientIdMax')
      : 'Client ID 长度不能超过 23 个字符',
    headerKeyRequired: t ? t('validation.headerKeyRequired') : '请输入标头名称',
    headerValueRequired: t
      ? t('validation.headerValueRequired')
      : '请输入标头值',
    headerValueReenterRequired: t
      ? t('validation.headerValueReenterRequired')
      : '修改或新增标头时，请重新填写此标头值，或将其移除',
    reservedHeader: (header: string) =>
      t
        ? t('validation.reservedHeader', { key: header })
        : `标头 "${header}" 为系统保留标头，禁止自定义`,
  }

  const headerItemSchema = z.object({
    key: z.string(),
    value: z.string(),
  })

  return z
    .object({
      name: z.string().trim().min(1, msg.nameRequired).max(64, msg.nameMax),
      channelType: z.enum(['webhook', 'mqtt']),
      eventsEnabled: z.boolean(),
      heartbeatEnabled: z.boolean(),
      imageMode: z.enum(['url', 'multipart', 'base64']),
      heartbeatIntervalSeconds: z.number({ error: msg.heartbeatIntervalRange }),
      // Webhook
      url: z.string(),
      heartbeatUrl: z.string(),
      authType: z.enum(['none', 'bearer', 'basic']),
      timeoutSeconds: z.number({ error: msg.timeoutRange }),
      token: z.string(),
      basicUser: z.string(),
      basicPass: z.string(),
      headers: z.array(headerItemSchema),
      // MQTT
      broker: z.string(),
      topic: z.string(),
      heartbeatTopic: z.string(),
      lwtEnabled: z.boolean(),
      clientId: z.string().max(23, msg.clientIdMax),
      qos: z.number().min(0).max(2),
      keepAliveSeconds: z.number({ error: msg.keepAliveRange }),
      mqttUser: z.string(),
      mqttPass: z.string(),
      tlsSkipVerify: z.boolean(),
      // Filter
      eventTypes: z.array(z.string()),
      algorithmIds: z.array(z.string()),
      cameraIds: z.array(z.string()),
    })
    .superRefine((data, ctx) => {
      // 0. 至少开启一项北向能力
      if (!data.eventsEnabled && !data.heartbeatEnabled) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['eventsEnabled'],
          message: msg.capabilityRequired,
        })
      }

      if (
        data.heartbeatEnabled &&
        (!Number.isInteger(data.heartbeatIntervalSeconds) ||
          data.heartbeatIntervalSeconds < 15 ||
          data.heartbeatIntervalSeconds > 3600)
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['heartbeatIntervalSeconds'],
          message: msg.heartbeatIntervalRange,
        })
      }

      // 1. Webhook 协议校验
      if (data.channelType === 'webhook') {
        if (
          !Number.isFinite(data.timeoutSeconds) ||
          data.timeoutSeconds < 1 ||
          data.timeoutSeconds > 30
        ) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['timeoutSeconds'],
            message: msg.timeoutRange,
          })
        }

        const trimmedUrl = data.url.trim()
        const mainUrlValid = isValidHttpUrlWithoutUserinfo(trimmedUrl)
        if (!trimmedUrl) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['url'],
            message: msg.urlRequired,
          })
        } else if (!mainUrlValid) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['url'],
            message: msg.urlInvalid,
          })
        }

        // 独立存活接收 URL 校验（仅在开启设备存活上报且填写时检查）
        const trimmedHbUrl = data.heartbeatUrl.trim()
        if (data.heartbeatEnabled && trimmedHbUrl) {
          if (!isValidHttpUrlWithoutUserinfo(trimmedHbUrl)) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['heartbeatUrl'],
              message: msg.urlInvalid,
            })
          } else {
            const hasCredentialsOrHeaders =
              data.authType !== 'none' ||
              data.headers.some((h) => h.key.trim() !== '')
            if (
              hasCredentialsOrHeaders &&
              mainUrlValid &&
              !isSameHttpOrigin(trimmedUrl, trimmedHbUrl)
            ) {
              ctx.addIssue({
                code: z.ZodIssueCode.custom,
                path: ['heartbeatUrl'],
                message: msg.heartbeatCrossOrigin,
              })
            }
          }
        }

        // Webhook Auth：仅在编辑态且未切换鉴权类型时，允许沿用已保存密钥
        const hasExistingSecret =
          isEdit &&
          secretRetained &&
          (!originalAuthType || data.authType === originalAuthType)
        if (data.authType === 'bearer') {
          if (!hasExistingSecret && !data.token.trim()) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['token'],
              message: msg.tokenRequired,
            })
          }
        } else if (data.authType === 'basic') {
          if (!hasExistingSecret && !data.basicUser.trim()) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['basicUser'],
              message: msg.basicUserRequired,
            })
          }
          if (!hasExistingSecret && !data.basicPass.trim()) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['basicPass'],
              message: msg.basicPassRequired,
            })
          }
        }

        // Webhook Headers
        const headersUntouched =
          isEdit && areHeadersUntouched(data.headers, originalHeaderNames)

        data.headers.forEach((h, idx) => {
          const trimmedKey = h.key.trim()
          const lowerKey = trimmedKey.toLowerCase()
          if (!trimmedKey) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['headers', idx, 'key'],
              message: msg.headerKeyRequired,
            })
          } else if (
            RESERVED_HEADERS.includes(
              lowerKey as (typeof RESERVED_HEADERS)[number]
            )
          ) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['headers', idx, 'key'],
              message: msg.reservedHeader(h.key),
            })
          }

          if (!headersUntouched && !h.value) {
            const isRetained =
              isEdit && originalHeaderNames.includes(trimmedKey)
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['headers', idx, 'value'],
              message: isRetained
                ? msg.headerValueReenterRequired
                : msg.headerValueRequired,
            })
          }
        })
      }

      // 2. MQTT 协议校验
      if (data.channelType === 'mqtt') {
        if (
          !Number.isFinite(data.keepAliveSeconds) ||
          data.keepAliveSeconds < 5 ||
          data.keepAliveSeconds > 300
        ) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['keepAliveSeconds'],
            message: msg.keepAliveRange,
          })
        }

        const trimmedBroker = data.broker.trim()
        if (!trimmedBroker) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['broker'],
            message: msg.brokerRequired,
          })
        } else {
          try {
            const parsed = new URL(trimmedBroker)
            if (parsed.username || parsed.password) {
              ctx.addIssue({
                code: z.ZodIssueCode.custom,
                path: ['broker'],
                message: msg.brokerInvalid,
              })
            }
          } catch {
            if (trimmedBroker.includes('@')) {
              ctx.addIssue({
                code: z.ZodIssueCode.custom,
                path: ['broker'],
                message: msg.brokerInvalid,
              })
            }
          }
        }

        const trimmedTopic = data.topic.trim()
        if (!trimmedTopic) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['topic'],
            message: msg.topicRequired,
          })
        } else if (trimmedTopic.includes('+') || trimmedTopic.includes('#')) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['topic'],
            message: msg.topicNoWildcards,
          })
        }

        const trimmedHbTopic = data.heartbeatTopic.trim()
        if (
          data.heartbeatEnabled &&
          trimmedHbTopic &&
          (trimmedHbTopic.includes('+') || trimmedHbTopic.includes('#'))
        ) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['heartbeatTopic'],
            message: msg.topicNoWildcards,
          })
        }
      }
    })
}

export type AlertDestinationFormValues = z.infer<
  ReturnType<typeof createAlertDestinationFormSchema>
>
