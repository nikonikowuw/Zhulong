import { describe, expect, it } from 'vitest'
import { createAlertDestinationFormSchema } from './schema'

describe('createAlertDestinationFormSchema', () => {
  it('validates a valid webhook destination form values', () => {
    const schema = createAlertDestinationFormSchema()
    const validWebhook = {
      name: 'SOC Forwarder',
      channelType: 'webhook' as const,
      imageMode: 'multipart' as const,
      url: 'https://api.example.com/alerts/webhook',
      authType: 'none' as const,
      timeoutSeconds: 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: [{ key: 'X-Custom-Token', value: 'secret' }],
      broker: '',
      topic: '',
      clientId: '',
      qos: 0,
      keepAliveSeconds: 30,
      mqttUser: '',
      mqttPass: '',
      tlsSkipVerify: false,
      eventTypes: ['alarm'],
      algorithmIds: ['algo-1'],
      cameraIds: ['cam-1'],
    }

    const res = schema.safeParse(validWebhook)
    expect(res.success).toBe(true)
  })

  it('rejects invalid webhook url with userinfo', () => {
    const schema = createAlertDestinationFormSchema()
    const invalidWebhook = {
      name: 'SOC Forwarder',
      channelType: 'webhook' as const,
      imageMode: 'multipart' as const,
      url: 'https://user:pass@api.example.com/alerts/webhook',
      authType: 'none' as const,
      timeoutSeconds: 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: [],
      broker: '',
      topic: '',
      clientId: '',
      qos: 0,
      keepAliveSeconds: 30,
      mqttUser: '',
      mqttPass: '',
      tlsSkipVerify: false,
      eventTypes: [],
      algorithmIds: [],
      cameraIds: [],
    }

    const res = schema.safeParse(invalidWebhook)
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(res.error.issues.some((i) => i.path.includes('url'))).toBe(true)
    }
  })

  it('rejects reserved headers in webhook custom headers', () => {
    const schema = createAlertDestinationFormSchema()
    const invalidHeaders = {
      name: 'SOC Forwarder',
      channelType: 'webhook' as const,
      imageMode: 'multipart' as const,
      url: 'https://api.example.com/alerts/webhook',
      authType: 'none' as const,
      timeoutSeconds: 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: [{ key: 'Authorization', value: 'override-bearer' }],
      broker: '',
      topic: '',
      clientId: '',
      qos: 0,
      keepAliveSeconds: 30,
      mqttUser: '',
      mqttPass: '',
      tlsSkipVerify: false,
      eventTypes: [],
      algorithmIds: [],
      cameraIds: [],
    }

    const res = schema.safeParse(invalidHeaders)
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(res.error.issues.some((i) => i.path.includes('headers'))).toBe(
        true
      )
    }
  })

  it('validates a valid mqtt destination form values', () => {
    const schema = createAlertDestinationFormSchema()
    const validMqtt = {
      name: 'IoT Gateway',
      channelType: 'mqtt' as const,
      imageMode: 'base64' as const,
      url: '',
      authType: 'none' as const,
      timeoutSeconds: 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: [],
      broker: 'tcp://10.0.0.1:1883',
      topic: 'zhulong/events/alarm',
      clientId: 'zhulong-edge-01',
      qos: 1,
      keepAliveSeconds: 60,
      mqttUser: 'admin',
      mqttPass: 'password',
      tlsSkipVerify: false,
      eventTypes: [],
      algorithmIds: [],
      cameraIds: [],
    }

    const res = schema.safeParse(validMqtt)
    expect(res.success).toBe(true)
  })

  it('rejects mqtt publish topic with wildcards', () => {
    const schema = createAlertDestinationFormSchema()
    const invalidMqtt = {
      name: 'IoT Gateway',
      channelType: 'mqtt' as const,
      imageMode: 'base64' as const,
      url: '',
      authType: 'none' as const,
      timeoutSeconds: 5,
      token: '',
      basicUser: '',
      basicPass: '',
      headers: [],
      broker: 'tcp://10.0.0.1:1883',
      topic: 'zhulong/events/+',
      clientId: '',
      qos: 0,
      keepAliveSeconds: 30,
      mqttUser: '',
      mqttPass: '',
      tlsSkipVerify: false,
      eventTypes: [],
      algorithmIds: [],
      cameraIds: [],
    }

    const res = schema.safeParse(invalidMqtt)
    expect(res.success).toBe(false)
    if (!res.success) {
      expect(res.error.issues.some((i) => i.path.includes('topic'))).toBe(true)
    }
  })
})
