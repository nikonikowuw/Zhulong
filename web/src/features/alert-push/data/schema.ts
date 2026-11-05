import { z } from 'zod'
import { type TFunction } from 'i18next'

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

/**
 * 告警推送目标表单校验 Schema 工厂
 */
export function createAlertDestinationFormSchema(
  t?: TFunction,
  isEdit = false,
  secretRetained = false
) {
  const msg = {
    nameRequired: t ? t('validation.nameRequired') : '请输入通道名称',
    nameMax: t ? t('validation.nameMax') : '通道名称不能超过 64 个字符',
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
      imageMode: z.enum(['url', 'multipart', 'base64']),
      // Webhook
      url: z.string(),
      authType: z.enum(['none', 'bearer', 'basic']),
      timeoutSeconds: z.number().min(1).max(30),
      token: z.string(),
      basicUser: z.string(),
      basicPass: z.string(),
      headers: z.array(headerItemSchema),
      // MQTT
      broker: z.string(),
      topic: z.string(),
      clientId: z.string().max(23, msg.clientIdMax),
      qos: z.number().min(0).max(2),
      keepAliveSeconds: z.number().min(5).max(300),
      mqttUser: z.string(),
      mqttPass: z.string(),
      tlsSkipVerify: z.boolean(),
      // Filter
      eventTypes: z.array(z.string()),
      algorithmIds: z.array(z.string()),
      cameraIds: z.array(z.string()),
    })
    .superRefine((data, ctx) => {
      // 1. Webhook 协议校验
      if (data.channelType === 'webhook') {
        const trimmedUrl = data.url.trim()
        if (!trimmedUrl) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['url'],
            message: msg.urlRequired,
          })
        } else {
          try {
            const parsed = new URL(trimmedUrl)
            if (
              (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') ||
              parsed.username ||
              parsed.password
            ) {
              ctx.addIssue({
                code: z.ZodIssueCode.custom,
                path: ['url'],
                message: msg.urlInvalid,
              })
            }
          } catch {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['url'],
              message: msg.urlInvalid,
            })
          }
        }

        // Webhook Auth
        const hasExistingSecret = isEdit && secretRetained
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
        data.headers.forEach((h, idx) => {
          const k = h.key.trim().toLowerCase()
          if (
            k &&
            RESERVED_HEADERS.includes(k as (typeof RESERVED_HEADERS)[number])
          ) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ['headers', idx, 'key'],
              message: msg.reservedHeader(h.key),
            })
          }
        })
      }

      // 2. MQTT 协议校验
      if (data.channelType === 'mqtt') {
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
      }
    })
}

export type AlertDestinationFormValues = z.infer<
  ReturnType<typeof createAlertDestinationFormSchema>
>
