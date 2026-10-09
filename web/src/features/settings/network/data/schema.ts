import { z } from 'zod'
import { type TFunction } from 'i18next'

const IPV4_REGEX =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

/**
 * 校验是否为合法 IPv4 地址
 */
export function isValidIPv4(val: string): boolean {
  if (!val || typeof val !== 'string') return false
  return IPV4_REGEX.test(val.trim())
}

/**
 * 校验是否为标准连续子网掩码 (如 255.255.255.0, 255.255.0.0 等)
 */
export function isValidSubnetMask(maskStr: string): boolean {
  if (!isValidIPv4(maskStr)) return false

  const parts = maskStr.trim().split('.').map(Number)
  if (parts.length !== 4) return false

  // 拼接成 32 位无符号整数
  const mask =
    ((parts[0] << 24) | (parts[1] << 16) | (parts[2] << 8) | parts[3]) >>> 0
  if (mask === 0) return false

  // 取反后必须是 (2^k - 1) 的形式，即 inverted & (inverted + 1) === 0
  const inverted = ~mask >>> 0
  return (inverted & (inverted + 1)) === 0
}

/**
 * 将逗号/空格分隔的 DNS 字符串解析为数组
 */
export function parseDnsString(dnsStr?: string): string[] {
  if (!dnsStr) return []
  return dnsStr
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

export function createInterfaceFormSchema(t?: TFunction) {
  const msg = {
    ipRequired: t
      ? t('validation.ipRequired', { ns: 'network' })
      : '静态模式下必须填写 IP 地址',
    invalidIpv4: t
      ? t('validation.invalidIpv4', { ns: 'network' })
      : '请输入合法的 IPv4 地址 (例如: 192.168.1.100)',
    maskRequired: t
      ? t('validation.maskRequired', { ns: 'network' })
      : '静态模式下必须填写子网掩码',
    invalidMask: t
      ? t('validation.invalidMask', { ns: 'network' })
      : '请输入合法的连续子网掩码 (例如: 255.255.255.0)',
    invalidGateway: t
      ? t('validation.invalidGateway', { ns: 'network' })
      : '请输入合法的网关 IPv4 地址',
    invalidDns: (ip: string) =>
      t
        ? t('validation.invalidDns', { ns: 'network', ip })
        : `无效的 DNS 服务器地址: ${ip}`,
  }

  return z
    .object({
      mode: z.enum(['dhcp', 'static']),
      ipAddress: z.string().optional(),
      subnetMask: z.string().optional(),
      gateway: z.string().optional(),
      dns: z.string().optional(),
      setDefault: z.boolean(),
    })
    .superRefine((data, ctx) => {
      if (data.mode === 'static') {
        const ip = data.ipAddress?.trim()
        if (!ip) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: msg.ipRequired,
            path: ['ipAddress'],
          })
        } else if (!isValidIPv4(ip)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: msg.invalidIpv4,
            path: ['ipAddress'],
          })
        }

        const mask = data.subnetMask?.trim()
        if (!mask) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: msg.maskRequired,
            path: ['subnetMask'],
          })
        } else if (!isValidSubnetMask(mask)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: msg.invalidMask,
            path: ['subnetMask'],
          })
        }

        const gw = data.gateway?.trim()
        if (gw && !isValidIPv4(gw)) {
          ctx.addIssue({
            code: z.ZodIssueCode.custom,
            message: msg.invalidGateway,
            path: ['gateway'],
          })
        }
      }

      if (data.dns && data.dns.trim()) {
        const list = parseDnsString(data.dns)
        for (const item of list) {
          if (!isValidIPv4(item)) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              message: msg.invalidDns(item),
              path: ['dns'],
            })
            break
          }
        }
      }
    })
}

export const interfaceFormSchema = createInterfaceFormSchema()
export type InterfaceFormValues = z.infer<typeof interfaceFormSchema>

export function createPingFormSchema(t?: TFunction) {
  return z.object({
    target: z
      .string()
      .min(
        1,
        t
          ? t('validation.pingTargetRequired', { ns: 'network' })
          : '请输入探测目标 IP 地址或域名'
      ),
  })
}

export const pingFormSchema = createPingFormSchema()
export type PingFormValues = z.infer<typeof pingFormSchema>
