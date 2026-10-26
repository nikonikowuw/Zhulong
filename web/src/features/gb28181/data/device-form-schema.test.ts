import { describe, expect, it } from 'vitest'
import {
  createGb28181DeviceFormSchema,
  toGb28181DevicePayload,
} from './device-form-schema'

describe('gb28181 device form validation', () => {
  const baseDevice = {
    deviceId: '34020000001320000001',
    name: '东门球机',
    password: 's3cret',
    transport: 'udp' as const,
    expiresSeconds: 3600,
  }

  it('requires a password when provisioning a new device', () => {
    const schema = createGb28181DeviceFormSchema(undefined, { isEdit: false })
    const result = schema.safeParse({ ...baseDevice, password: '' })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['password'])
  })

  it('keeps the password optional when editing', () => {
    const schema = createGb28181DeviceFormSchema(undefined, { isEdit: true })
    const result = schema.safeParse({ ...baseDevice, password: '' })

    expect(result.success).toBe(true)
  })

  it('rejects a device ID that is not 20 digits', () => {
    const schema = createGb28181DeviceFormSchema(undefined, { isEdit: false })
    const result = schema.safeParse({
      ...baseDevice,
      deviceId: '3402000000132000000',
    })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['deviceId'])
  })

  it('bounds the registration lease', () => {
    const schema = createGb28181DeviceFormSchema(undefined, { isEdit: false })

    expect(
      schema.safeParse({ ...baseDevice, expiresSeconds: 59 }).success
    ).toBe(false)
    expect(
      schema.safeParse({ ...baseDevice, expiresSeconds: 86401 }).success
    ).toBe(false)
    expect(
      schema.safeParse({ ...baseDevice, expiresSeconds: undefined }).success
    ).toBe(false)
  })
})

describe('gb28181 device payload', () => {
  it('submits an explicitly cleared name but omits an untouched password', () => {
    // name 必须显式提交：后端把该字段声明为指针（省略即保留旧值），
    // 若空名字也省略，用户就永远删不掉写错的设备名。
    const payload = toGb28181DevicePayload({
      deviceId: '34020000001320000001',
      name: '  ',
      password: '',
      transport: 'tcp',
      expiresSeconds: 600,
    })

    expect(payload.name).toBe('')
    expect(payload.password).toBeUndefined()
    expect(payload.transport).toBe('tcp')
  })
})
