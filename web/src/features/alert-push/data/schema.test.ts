import { describe, expect, it } from 'vitest'
import {
  createAlertDestinationFormSchema,
  isSameHttpOrigin,
  type AlertDestinationFormValues,
} from './schema'

function makeBaseWebhookValues(
  overrides: Partial<AlertDestinationFormValues> = {}
): AlertDestinationFormValues {
  return {
    name: 'SOC Forwarder',
    channelType: 'webhook',
    eventsEnabled: true,
    heartbeatEnabled: false,
    imageMode: 'multipart',
    heartbeatIntervalSeconds: 60,
    url: 'https://api.example.com/alerts/webhook',
    heartbeatUrl: '',
    authType: 'none',
    timeoutSeconds: 5,
    token: '',
    basicUser: '',
    basicPass: '',
    headers: [],
    broker: '',
    topic: '',
    heartbeatTopic: '',
    lwtEnabled: true,
    clientId: '',
    qos: 0,
    keepAliveSeconds: 30,
    mqttUser: '',
    mqttPass: '',
    tlsSkipVerify: false,
    eventTypes: ['alarm'],
    algorithmIds: ['algo-1'],
    cameraIds: ['cam-1'],
    ...overrides,
  }
}

describe('createAlertDestinationFormSchema', () => {
  it('validates a valid webhook destination form values', () => {
    const schema = createAlertDestinationFormSchema()
    const res = schema.safeParse(
      makeBaseWebhookValues({
        headers: [{ key: 'X-Custom-Token', value: 'secret' }],
      })
    )
    expect(res.success).toBe(true)
  })

  it('rejects when both eventsEnabled and heartbeatEnabled are false', () => {
    const schema = createAlertDestinationFormSchema()
    const res = schema.safeParse(
      makeBaseWebhookValues({
        eventsEnabled: false,
        heartbeatEnabled: false,
      })
    )
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(
        res.error.issues.some((i) => i.path.includes('eventsEnabled'))
      ).toBe(true)
    }
  })

  it('validates heartbeat-only webhook destination and enforces same-origin when credentials exist', () => {
    const schema = createAlertDestinationFormSchema()

    // Same origin with implicit default 443 port -> valid
    const sameOriginRes = schema.safeParse(
      makeBaseWebhookValues({
        eventsEnabled: false,
        heartbeatEnabled: true,
        heartbeatIntervalSeconds: 30,
        url: 'https://api.example.com/v1/events',
        heartbeatUrl: 'https://API.example.com:443/v1/heartbeat',
        authType: 'bearer',
        token: 'secret-token',
      })
    )
    expect(sameOriginRes.success).toBe(true)

    // Cross-origin with bearer credentials -> rejected
    const crossOriginRes = schema.safeParse(
      makeBaseWebhookValues({
        eventsEnabled: false,
        heartbeatEnabled: true,
        url: 'https://api.example.com/v1/events',
        heartbeatUrl: 'https://evil.other.org/v1/heartbeat',
        authType: 'bearer',
        token: 'secret-token',
      })
    )
    expect(crossOriginRes.success).toBe(false)
    if (!crossOriginRes.success) {
      expect(
        crossOriginRes.error.issues.some((i) => i.path.includes('heartbeatUrl'))
      ).toBe(true)
    }
  })

  it('rejects out-of-range heartbeatIntervalSeconds', () => {
    const schema = createAlertDestinationFormSchema()
    const res = schema.safeParse(
      makeBaseWebhookValues({
        heartbeatEnabled: true,
        heartbeatIntervalSeconds: 10,
      })
    )
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(
        res.error.issues.some((i) =>
          i.path.includes('heartbeatIntervalSeconds')
        )
      ).toBe(true)
    }
  })

  it('rejects invalid webhook url with userinfo', () => {
    const schema = createAlertDestinationFormSchema()
    const res = schema.safeParse(
      makeBaseWebhookValues({
        url: 'https://user:pass@api.example.com/alerts/webhook',
      })
    )
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(res.error.issues.some((i) => i.path.includes('url'))).toBe(true)
    }
  })

  it('rejects reserved headers in webhook custom headers', () => {
    const schema = createAlertDestinationFormSchema()
    const res = schema.safeParse(
      makeBaseWebhookValues({
        headers: [{ key: 'Authorization', value: 'override-bearer' }],
      })
    )
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(res.error.issues.some((i) => i.path.includes('headers'))).toBe(
        true
      )
    }
  })

  it('validates mqtt destination and rejects wildcards in topic or heartbeatTopic', () => {
    const schema = createAlertDestinationFormSchema()
    const validMqtt = makeBaseWebhookValues({
      name: 'IoT Gateway',
      channelType: 'mqtt',
      imageMode: 'base64',
      heartbeatEnabled: true,
      url: '',
      broker: 'tcp://10.0.0.1:1883',
      topic: 'zhulong/events/alarm',
      heartbeatTopic: 'zhulong/status/box01',
      clientId: 'zhulong-edge-01',
      qos: 1,
      keepAliveSeconds: 60,
      mqttUser: 'admin',
      mqttPass: 'password',
    })

    expect(schema.safeParse(validMqtt).success).toBe(true)

    const wildcardMain = schema.safeParse({
      ...validMqtt,
      topic: 'zhulong/events/+',
    })
    expect(wildcardMain.success).toBe(false)

    const wildcardHeartbeat = schema.safeParse({
      ...validMqtt,
      heartbeatTopic: 'zhulong/status/#',
    })
    expect(wildcardHeartbeat.success).toBe(false)
    if (!wildcardHeartbeat.success) {
      expect(
        wildcardHeartbeat.error.issues.some((i) =>
          i.path.includes('heartbeatTopic')
        )
      ).toBe(true)
    }
  })

  it('compares HTTP origins strictly across scheme, host, and default port', () => {
    expect(
      isSameHttpOrigin('https://example.com/a', 'https://EXAMPLE.com:443/b')
    ).toBe(true)
    expect(
      isSameHttpOrigin('https://example.com/a', 'http://example.com/b')
    ).toBe(false)
    expect(
      isSameHttpOrigin('https://example.com:8443/a', 'https://example.com/b')
    ).toBe(false)
  })

  it('requires new credentials in edit mode when switching authType away from originalAuthType', () => {
    const schema = createAlertDestinationFormSchema(
      undefined,
      true,
      true,
      'bearer'
    )

    // Keeping originalAuthType ('bearer') allows blank token
    const keepBearer = schema.safeParse(
      makeBaseWebhookValues({ authType: 'bearer', token: '' })
    )
    expect(keepBearer.success).toBe(true)

    // Switching from 'bearer' to 'basic' must require basicUser & basicPass
    const switchToBasic = schema.safeParse(
      makeBaseWebhookValues({
        authType: 'basic',
        basicUser: '',
        basicPass: '',
      })
    )
    expect(switchToBasic.success).toBe(false)
    if (!switchToBasic.success) {
      expect(
        switchToBasic.error.issues.some((i) => i.path.includes('basicUser'))
      ).toBe(true)
      expect(
        switchToBasic.error.issues.some((i) => i.path.includes('basicPass'))
      ).toBe(true)
    }
  })

  it('handles retained custom headers vs modified/empty headers in edit and create modes', () => {
    const editSchema = createAlertDestinationFormSchema(
      undefined,
      true,
      true,
      'none',
      ['X-A', 'X-B']
    )

    // Untouched retained headers with empty values -> allowed
    expect(
      editSchema.safeParse(
        makeBaseWebhookValues({
          headers: [
            { key: 'X-A', value: '' },
            { key: 'X-B', value: '' },
          ],
        })
      ).success
    ).toBe(true)

    // Clearing all headers -> allowed (submit will send {})
    expect(
      editSchema.safeParse(makeBaseWebhookValues({ headers: [] })).success
    ).toBe(true)

    // Updating X-B while leaving X-A blank -> rejected so X-A is not silently dropped
    const partialReenter = editSchema.safeParse(
      makeBaseWebhookValues({
        headers: [
          { key: 'X-A', value: '' },
          { key: 'X-B', value: 'new-secret' },
        ],
      })
    )
    expect(partialReenter.success).toBe(false)
    if (!partialReenter.success) {
      expect(
        partialReenter.error.issues.some(
          (i) => i.path.join('.') === 'headers.0.value'
        )
      ).toBe(true)
    }

    // Create mode rejects blank header key or value
    const createSchema = createAlertDestinationFormSchema()
    const emptyCreateHeader = createSchema.safeParse(
      makeBaseWebhookValues({
        headers: [{ key: 'X-New', value: '' }],
      })
    )
    expect(emptyCreateHeader.success).toBe(false)
  })

  it('rejects NaN or out-of-range timeoutSeconds and keepAliveSeconds', () => {
    const schema = createAlertDestinationFormSchema()

    const badTimeout = schema.safeParse(
      makeBaseWebhookValues({ timeoutSeconds: Number.NaN })
    )
    expect(badTimeout.success).toBe(false)
    if (!badTimeout.success) {
      expect(
        badTimeout.error.issues.some((i) => i.path.includes('timeoutSeconds'))
      ).toBe(true)
    }

    const badKeepAlive = schema.safeParse(
      makeBaseWebhookValues({
        channelType: 'mqtt',
        broker: 'tcp://10.0.0.1:1883',
        topic: 'zhulong/alarms',
        keepAliveSeconds: 2,
      })
    )
    expect(badKeepAlive.success).toBe(false)
    if (!badKeepAlive.success) {
      expect(
        badKeepAlive.error.issues.some((i) =>
          i.path.includes('keepAliveSeconds')
        )
      ).toBe(true)
    }
  })
})
