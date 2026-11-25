import { describe, expect, it } from 'vitest'
import {
  createAlertDestinationFormSchema,
  INLINE_MAX_KB_DEFAULT,
  isSameHttpOrigin,
  RESERVED_HEADERS,
  RESERVED_WEBHOOK_HEADERS,
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
    signingSecret: '',
    headers: [],
    broker: '',
    topic: '',
    heartbeatTopic: '',
    lwtEnabled: true,
    clientId: '',
    qos: 0,
    keepAliveSeconds: 30,
    inlineMaxKb: 500,
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

    // Cross-origin with authType='none' + new signingSecret -> rejected
    const crossOriginSigningRes = schema.safeParse(
      makeBaseWebhookValues({
        eventsEnabled: false,
        heartbeatEnabled: true,
        url: 'https://api.example.com/v1/events',
        heartbeatUrl: 'https://evil.other.org/v1/heartbeat',
        authType: 'none',
        signingSecret: 'hmac-secret-key',
      })
    )
    expect(crossOriginSigningRes.success).toBe(false)
    if (!crossOriginSigningRes.success) {
      expect(
        crossOriginSigningRes.error.issues.some((i) =>
          i.path.includes('heartbeatUrl')
        )
      ).toBe(true)
    }

    // Edit mode with retained signingSecretSet (not cleared) -> cross-origin rejected
    const editRetainedSigningSchema = createAlertDestinationFormSchema(
      undefined,
      true,
      false,
      'none',
      [],
      true
    )
    const crossOriginRetainedSigning = editRetainedSigningSchema.safeParse(
      makeBaseWebhookValues({
        eventsEnabled: false,
        heartbeatEnabled: true,
        url: 'https://api.example.com/v1/events',
        heartbeatUrl: 'https://evil.other.org/v1/heartbeat',
        authType: 'none',
        signingSecret: '',
      })
    )
    expect(crossOriginRetainedSigning.success).toBe(false)
    if (!crossOriginRetainedSigning.success) {
      expect(
        crossOriginRetainedSigning.error.issues.some((i) =>
          i.path.includes('heartbeatUrl')
        )
      ).toBe(true)
    }

    // Edit mode with signingSecret cleared (signingSecretRetained=false) -> cross-origin allowed
    const editClearedSigningSchema = createAlertDestinationFormSchema(
      undefined,
      true,
      false,
      'none',
      [],
      false
    )
    expect(
      editClearedSigningSchema.safeParse(
        makeBaseWebhookValues({
          eventsEnabled: false,
          heartbeatEnabled: true,
          url: 'https://api.example.com/v1/events',
          heartbeatUrl: 'https://other.example.org/v1/heartbeat',
          authType: 'none',
          signingSecret: '',
        })
      ).success
    ).toBe(true)
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

  it('rejects reserved headers and case-insensitive duplicate headers in webhook custom headers', () => {
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

    const dupRes = schema.safeParse(
      makeBaseWebhookValues({
        headers: [
          { key: 'X-Tenant-Id', value: 't1' },
          { key: 'x-tenant-id', value: 't2' },
        ],
      })
    )
    expect(dupRes.success).toBe(false)
    if (!dupRes.success) {
      expect(
        dupRes.error.issues.some((i) => i.path.join('.') === 'headers.1.key')
      ).toBe(true)
    }
  })

  it('validates mqtt broker scheme/host/userinfo and rejects wildcards in topic or heartbeatTopic', () => {
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

    for (const allowedBroker of [
      'tcp://10.0.0.1:1883',
      'ssl://broker.local:8883',
      'tls://broker.local:8883',
      'tcps://broker.local:8883',
      'ws://broker.local:8083/mqtt',
      'wss://broker.local:8084/mqtt',
      'mqtt://10.0.0.1:1883',
      'mqtts://broker.local:8883',
    ]) {
      expect(
        schema.safeParse({ ...validMqtt, broker: allowedBroker }).success
      ).toBe(true)
    }

    for (const invalidBroker of [
      '192.168.1.100:1883',
      'http://10.0.0.1:1883',
      'tcp://',
      'tcp://user:pass@10.0.0.1:1883',
      'tcp://:@10.0.0.1:1883',
    ]) {
      const bad = schema.safeParse({ ...validMqtt, broker: invalidBroker })
      expect(bad.success).toBe(false)
      if (!bad.success) {
        expect(bad.error.issues.some((i) => i.path.includes('broker'))).toBe(
          true
        )
      }
    }

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

  it('enforces UTF-8 byte length (<=23 bytes after trim) on MQTT clientId', () => {
    const schema = createAlertDestinationFormSchema()
    const baseMqtt = makeBaseWebhookValues({
      channelType: 'mqtt',
      imageMode: 'base64',
      url: '',
      broker: 'tcp://10.0.0.1:1883',
      topic: 'zhulong/alarms',
    })

    // 7 CJK chars = 21 UTF-8 bytes <= 23 (with surrounding spaces trimmed) -> valid
    expect(
      schema.safeParse({ ...baseMqtt, clientId: '  边缘设备一二三  ' }).success
    ).toBe(true)

    // 8 CJK chars = 24 UTF-8 bytes > 23 (even though JS .length === 8) -> rejected
    const tooLongCjk = schema.safeParse({
      ...baseMqtt,
      clientId: '边缘设备一二三四',
    })
    expect(tooLongCjk.success).toBe(false)
    if (!tooLongCjk.success) {
      expect(
        tooLongCjk.error.issues.some((i) => i.path.includes('clientId'))
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

  it('rejects NaN, decimals, or out-of-range timeoutSeconds and keepAliveSeconds', () => {
    const schema = createAlertDestinationFormSchema()

    for (const badVal of [Number.NaN, 5.5, 0, 31]) {
      const badTimeout = schema.safeParse(
        makeBaseWebhookValues({ timeoutSeconds: badVal })
      )
      expect(badTimeout.success).toBe(false)
      if (!badTimeout.success) {
        expect(
          badTimeout.error.issues.some((i) => i.path.includes('timeoutSeconds'))
        ).toBe(true)
      }
    }

    for (const badVal of [Number.NaN, 30.5, 2, 301]) {
      const badKeepAlive = schema.safeParse(
        makeBaseWebhookValues({
          channelType: 'mqtt',
          imageMode: 'base64',
          broker: 'tcp://10.0.0.1:1883',
          topic: 'zhulong/alarms',
          keepAliveSeconds: badVal,
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
    }
  })

  it('rejects incompatible protocol x imageMode combinations when events are enabled', () => {
    const schema = createAlertDestinationFormSchema()

    const webhookBase64 = schema.safeParse(
      makeBaseWebhookValues({
        channelType: 'webhook',
        eventsEnabled: true,
        imageMode: 'base64',
      })
    )
    expect(webhookBase64.success).toBe(false)

    const mqttMultipart = schema.safeParse(
      makeBaseWebhookValues({
        channelType: 'mqtt',
        eventsEnabled: true,
        imageMode: 'multipart',
        broker: 'tcp://10.0.0.1:1883',
        topic: 'zhulong/alarms',
      })
    )
    expect(mqttMultipart.success).toBe(false)
  })

  it('validates inlineMaxKb range only in MQTT base64 mode', () => {
    const schema = createAlertDestinationFormSchema()
    const mqttBase64 = (inlineMaxKb: number) =>
      makeBaseWebhookValues({
        channelType: 'mqtt',
        imageMode: 'base64',
        url: '',
        broker: 'tcp://10.0.0.1:1883',
        topic: 'zhulong/alarms',
        inlineMaxKb,
      })

    expect(INLINE_MAX_KB_DEFAULT).toBe(500)
    for (const ok of [64, 500, 8192]) {
      expect(schema.safeParse(mqttBase64(ok)).success).toBe(true)
    }
    for (const bad of [63, 8193, 100.5, Number.NaN]) {
      const res = schema.safeParse(mqttBase64(bad))
      expect(res.success).toBe(false)
      if (!res.success) {
        expect(
          res.error.issues.some((i) => i.path.includes('inlineMaxKb'))
        ).toBe(true)
      }
    }

    const urlMode = schema.safeParse({
      ...mqttBase64(1),
      imageMode: 'url',
    })
    expect(urlMode.success).toBe(true)
  })

  it('reserves the 13 v1 envelope, HMAC signature, and transport headers', () => {
    expect(RESERVED_WEBHOOK_HEADERS).toBe(RESERVED_HEADERS)
    expect(RESERVED_HEADERS).toHaveLength(13)
    expect(RESERVED_HEADERS).toEqual(
      expect.arrayContaining([
        'accept',
        'authorization',
        'connection',
        'content-length',
        'content-type',
        'host',
        'transfer-encoding',
        'user-agent',
        'x-zhulong-event',
        'x-zhulong-delivery',
        'x-zhulong-schema',
        'x-zhulong-timestamp',
        'x-zhulong-signature',
      ])
    )
    const schema = createAlertDestinationFormSchema()
    for (const key of [
      'Transfer-Encoding',
      'X-Zhulong-Timestamp',
      'X-Zhulong-Signature',
    ]) {
      const res = schema.safeParse(
        makeBaseWebhookValues({
          headers: [{ key, value: 'tamper' }],
        })
      )
      expect(res.success).toBe(false)
    }
  })
})
