import { z } from 'zod'
import { type TFunction } from 'i18next'
import { createMessageHelper } from './form-message'
import {
  DEVICE_EXPIRES_MAX,
  DEVICE_EXPIRES_MIN,
  DEVICE_NAME_MAX_LENGTH,
  DEVICE_PASSWORD_MAX_LENGTH,
  DEVICE_TRANSPORTS,
  isValidDeviceID,
  type DeviceTransport,
  type Gb28181Device,
  type UpsertGb28181DeviceRequest,
} from './schema'

/**
 * 设备预置/凭据轮换表单的 schema 与请求体映射。
 */

// --- 设备预置表单 ---

export function createGb28181DeviceFormSchema(
  t?: TFunction,
  options: { isEdit?: boolean } = {}
) {
  const message = createMessageHelper(t)
  const isEdit = options.isEdit ?? false

  return z
    .object({
      deviceId: z.string(),
      name: z.string().max(
        DEVICE_NAME_MAX_LENGTH,
        message('deviceNameMax', {
          max: DEVICE_NAME_MAX_LENGTH,
        })
      ),
      password: z
        .string()
        .max(
          DEVICE_PASSWORD_MAX_LENGTH,
          message('passwordMax', { max: DEVICE_PASSWORD_MAX_LENGTH })
        ),
      transport: z.enum(DEVICE_TRANSPORTS),
      expiresSeconds: z.number().optional(),
    })
    .superRefine((values, ctx) => {
      if (!isValidDeviceID(values.deviceId)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['deviceId'],
          message: message('deviceIdFormat'),
        })
      }
      if (!isEdit && values.password === '') {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['password'],
          message: message('passwordRequired'),
        })
      }

      const { expiresSeconds } = values
      if (expiresSeconds === undefined) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['expiresSeconds'],
          message: message('expiresRequired'),
        })
        return
      }
      if (
        !Number.isInteger(expiresSeconds) ||
        expiresSeconds < DEVICE_EXPIRES_MIN ||
        expiresSeconds > DEVICE_EXPIRES_MAX
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['expiresSeconds'],
          message: message('expiresRange', {
            min: DEVICE_EXPIRES_MIN,
            max: DEVICE_EXPIRES_MAX,
          }),
        })
      }
    })
}

export type Gb28181DeviceFormValues = z.infer<
  ReturnType<typeof createGb28181DeviceFormSchema>
>

export function toGb28181DeviceFormValues(
  device: Gb28181Device | null
): Gb28181DeviceFormValues {
  return {
    deviceId: device?.deviceId ?? '',
    name: device?.name ?? '',
    // 密码永不回显：编辑时留空即表示沿用已存凭据。
    password: '',
    transport: DEVICE_TRANSPORTS.includes(device?.transport as DeviceTransport)
      ? (device?.transport as DeviceTransport)
      : 'udp',
    expiresSeconds: device?.expiresSeconds,
  }
}

/**
 * 表单值 → 设备 upsert 请求体。
 *
 * 名字始终显式提交：后端把 `name` 声明成指针（省略即保留旧值），若前端在
 * 名字为空时省略字段，用户就永远删不掉一个写错的设备名，而界面上又写着
 * 「留空即保留」只针对密码。密码相反 —— 输入框不回显，留空只能是「沿用」。
 */
export function toGb28181DevicePayload(
  values: Gb28181DeviceFormValues
): UpsertGb28181DeviceRequest {
  return {
    name: values.name.trim(),
    password: values.password === '' ? undefined : values.password,
    transport: values.transport,
    expiresSeconds: values.expiresSeconds,
  }
}
