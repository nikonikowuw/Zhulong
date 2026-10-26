import { z } from 'zod'
import { type TFunction } from 'i18next'
import { createMessageHelper } from './form-message'
import {
  IPV4_PATTERN,
  MEDIA_ADDRESS_MAX_LENGTH,
  mediaCapacity,
  parseSipListen,
  RTP_PORT_MAX,
  RTP_PORT_MIN,
  SIP_IDENTITY_MAX_LENGTH,
  SIP_IDENTITY_PATTERN,
  SIP_TRANSPORTS,
  SUPPORTED_SECURITY_PROFILE,
  type SipTransport,
  type Gb28181Config,
} from './schema'

/**
 * 配置表单的 schema 与双向映射（表单值 ⇄ 服务端配置）。
 *
 * 与 `schema.ts` 的 API 契约分开：契约随后端变，表单随交互变。
 */

// --- 配置表单 ---

export function createGb28181ConfigFormSchema(t?: TFunction) {
  const message = createMessageHelper(t)

  return z
    .object({
      enabled: z.boolean(),
      sipId: z.string().max(
        SIP_IDENTITY_MAX_LENGTH,
        message('maxLength', {
          max: SIP_IDENTITY_MAX_LENGTH,
        })
      ),
      sipRealm: z.string().max(
        SIP_IDENTITY_MAX_LENGTH,
        message('maxLength', {
          max: SIP_IDENTITY_MAX_LENGTH,
        })
      ),
      sipListen: z.string().max(
        SIP_IDENTITY_MAX_LENGTH,
        message('maxLength', {
          max: SIP_IDENTITY_MAX_LENGTH,
        })
      ),
      sipTransport: z.enum(SIP_TRANSPORTS),
      mediaBindAddress: z.string().max(
        MEDIA_ADDRESS_MAX_LENGTH,
        message('maxLength', {
          max: MEDIA_ADDRESS_MAX_LENGTH,
        })
      ),
      mediaAdvertisedIp: z.string().max(
        MEDIA_ADDRESS_MAX_LENGTH,
        message('maxLength', {
          max: MEDIA_ADDRESS_MAX_LENGTH,
        })
      ),
      // 数字框允许「留空」（NumberInput 提交 undefined），
      // 因此必填与范围必须在对象级校验里给出可本地化的文案。
      rtpPortMin: z.number().optional(),
      rtpPortMax: z.number().optional(),
    })
    .superRefine((values, ctx) => {
      const sipId = values.sipId.trim()
      const sipRealm = values.sipRealm.trim()
      const sipListen = values.sipListen.trim()
      const bindAddress = values.mediaBindAddress.trim()
      const advertisedIp = values.mediaAdvertisedIp.trim()

      if (sipId === '') {
        if (values.enabled) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['sipId'],
            message: message('sipIdRequired'),
          })
        }
      } else if (!SIP_IDENTITY_PATTERN.test(sipId)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['sipId'],
          message: message('identityCharset'),
        })
      }

      if (sipRealm !== '' && !SIP_IDENTITY_PATTERN.test(sipRealm)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['sipRealm'],
          message: message('identityCharset'),
        })
      }

      if (sipListen === '') {
        if (values.enabled) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['sipListen'],
            message: message('listenRequired'),
          })
        }
      } else {
        const parsed = parseSipListen(sipListen)
        if (!parsed) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['sipListen'],
            message: message('listenFormat'),
          })
        } else if (parsed.port < RTP_PORT_MIN || parsed.port > RTP_PORT_MAX) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['sipListen'],
            message: message('listenPortRange'),
          })
        }
      }

      if (bindAddress === '') {
        if (values.enabled) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['mediaBindAddress'],
            message: message('bindAddressRequired'),
          })
        }
      } else if (!IPV4_PATTERN.test(bindAddress)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['mediaBindAddress'],
          message: message('ipv4Invalid'),
        })
      }

      if (advertisedIp !== '' && !IPV4_PATTERN.test(advertisedIp)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['mediaAdvertisedIp'],
          message: message('ipv4Invalid'),
        })
      }

      const { rtpPortMin, rtpPortMax } = values
      for (const [path, port] of [
        ['rtpPortMin', rtpPortMin],
        ['rtpPortMax', rtpPortMax],
      ] as const) {
        if (port === undefined) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: [path],
            message: message('portRequired'),
          })
          continue
        }
        if (
          !Number.isInteger(port) ||
          port < RTP_PORT_MIN ||
          port > RTP_PORT_MAX
        ) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: [path],
            message: message('portRange', {
              min: RTP_PORT_MIN,
              max: RTP_PORT_MAX,
            }),
          })
        }
      }

      if (rtpPortMin !== undefined && rtpPortMax !== undefined) {
        if (rtpPortMin > rtpPortMax) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['rtpPortMax'],
            message: message('portOrder'),
          })
        } else if (mediaCapacity(rtpPortMin, rtpPortMax) < 1) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            path: ['rtpPortMax'],
            message: message('portSpan'),
          })
        }
      }
    })
}

export type Gb28181ConfigFormValues = z.infer<
  ReturnType<typeof createGb28181ConfigFormSchema>
>

/** 把服务端配置投影到表单值；端口 0 表示「未配置」，交给表单重新填写。 */
export function toGb28181ConfigFormValues(
  config: Gb28181Config
): Gb28181ConfigFormValues {
  const transport = SIP_TRANSPORTS.includes(config.sipTransport as SipTransport)
    ? (config.sipTransport as SipTransport)
    : 'both'

  return {
    enabled: config.enabled,
    sipId: config.sipId,
    sipRealm: config.sipRealm,
    sipListen: config.sipListen,
    sipTransport: transport,
    mediaBindAddress: config.mediaBindAddress,
    mediaAdvertisedIp: config.mediaAdvertisedIp,
    rtpPortMin: config.rtpPortMin > 0 ? config.rtpPortMin : undefined,
    rtpPortMax: config.rtpPortMax > 0 ? config.rtpPortMax : undefined,
  }
}

/**
 * 表单值 → PUT 请求体。
 *
 * 安全档位与「仅允许预置设备注册」在 P0 固定：后端会以
 * `GB28181_PROFILE_UNSUPPORTED` / `GB28181_REGISTERED_DEVICE_REQUIRED`
 * 拒绝其他取值，因此这里不把它们暴露成可编辑字段，但必须随请求发送。
 */
export function toGb28181ConfigPayload(
  values: Gb28181ConfigFormValues
): Gb28181Config {
  return {
    enabled: values.enabled,
    sipId: values.sipId.trim(),
    sipRealm: values.sipRealm.trim(),
    sipListen: values.sipListen.trim(),
    sipTransport: values.sipTransport,
    securityProfile: SUPPORTED_SECURITY_PROFILE,
    mediaBindAddress: values.mediaBindAddress.trim(),
    mediaAdvertisedIp: values.mediaAdvertisedIp.trim(),
    rtpPortMin: values.rtpPortMin ?? 0,
    rtpPortMax: values.rtpPortMax ?? 0,
    requireRegisteredDevice: true,
  }
}
