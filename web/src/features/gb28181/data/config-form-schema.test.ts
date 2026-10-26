import { describe, expect, it } from 'vitest'
import {
  createGb28181ConfigFormSchema,
  toGb28181ConfigPayload,
} from './config-form-schema'

const configFormValues = {
  enabled: true,
  sipId: '34020000002000000001',
  sipRealm: '3402000000',
  sipListen: '0.0.0.0:5060',
  sipTransport: 'both' as const,
  mediaBindAddress: '0.0.0.0',
  mediaAdvertisedIp: '192.168.1.10',
  rtpPortMin: 30000,
  rtpPortMax: 30100,
}

describe('gb28181 config form validation', () => {
  const schema = createGb28181ConfigFormSchema()

  it('accepts a complete enabled configuration', () => {
    expect(schema.safeParse(configFormValues).success).toBe(true)
  })

  it('requires identity fields while enabled', () => {
    const result = schema.safeParse({
      ...configFormValues,
      sipId: '',
      sipListen: '',
      mediaBindAddress: '',
    })

    expect(result.success).toBe(false)
    const paths = result.error?.issues.map((issue) => issue.path[0])
    expect(paths).toContain('sipId')
    expect(paths).toContain('sipListen')
    expect(paths).toContain('mediaBindAddress')
  })

  it('allows an empty identity draft while disabled', () => {
    const result = schema.safeParse({
      ...configFormValues,
      enabled: false,
      sipId: '',
      sipListen: '',
      mediaBindAddress: '',
    })

    expect(result.success).toBe(true)
  })

  it('rejects an identity with characters outside the allowed set', () => {
    const result = schema.safeParse({
      ...configFormValues,
      sipId: '3402000000200000000/',
    })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['sipId'])
  })

  it('rejects an unparsable listen address', () => {
    const result = schema.safeParse({
      ...configFormValues,
      sipListen: '0.0.0.0',
    })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['sipListen'])
  })

  it('rejects an inverted port range', () => {
    const result = schema.safeParse({
      ...configFormValues,
      rtpPortMin: 30200,
      rtpPortMax: 30100,
    })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['rtpPortMax'])
  })

  it('rejects a range that cannot hold one RTP/RTCP pair', () => {
    const result = schema.safeParse({
      ...configFormValues,
      rtpPortMin: 30000,
      rtpPortMax: 30000,
    })

    expect(result.success).toBe(false)
    expect(result.error?.issues[0].path).toEqual(['rtpPortMax'])
  })

  it('requires both ports even when disabled', () => {
    const result = schema.safeParse({
      ...configFormValues,
      rtpPortMin: undefined,
      rtpPortMax: undefined,
    })

    expect(result.success).toBe(false)
    const paths = result.error?.issues.map((issue) => issue.path[0])
    expect(paths).toEqual(['rtpPortMin', 'rtpPortMax'])
  })
})

describe('gb28181 config payload', () => {
  it('pins the P0-fixed fields and trims user input', () => {
    const payload = toGb28181ConfigPayload({
      ...configFormValues,
      sipId: '  34020000002000000001 ',
      mediaAdvertisedIp: '',
    })

    expect(payload.sipId).toBe('34020000002000000001')
    expect(payload.securityProfile).toBe('standard')
    expect(payload.requireRegisteredDevice).toBe(true)
    expect(payload.mediaAdvertisedIp).toBe('')
  })
})
