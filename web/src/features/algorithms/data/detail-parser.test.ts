import { describe, it, expect } from 'vitest'
import { parseConfigSchema, parseManifest } from './detail-parser'

describe('detail-parser', () => {
  describe('parseConfigSchema', () => {
    it('handles empty or blank input as zero config', () => {
      expect(parseConfigSchema('')).toEqual({
        isZeroConfig: true,
        isValidJson: true,
        rawFormatted: '{}',
        properties: [],
      })
      expect(parseConfigSchema('   ').isZeroConfig).toBe(true)
      expect(parseConfigSchema('{}').isZeroConfig).toBe(true)
    })

    it('handles invalid json gracefully', () => {
      const res = parseConfigSchema('{ invalid json')
      expect(res.isValidJson).toBe(false)
      expect(res.isZeroConfig).toBe(true)
      expect(res.rawFormatted).toBe('{ invalid json')
    })

    it('parses valid properties and ranges', () => {
      const schema = JSON.stringify({
        type: 'object',
        required: ['threshold'],
        properties: {
          threshold: {
            type: 'number',
            title: '置信度阈值',
            description: '过滤目标的置信度',
            default: 0.5,
            minimum: 0.1,
            maximum: 0.9,
            step: 0.05,
          },
          mode: {
            type: 'string',
            title: '运行模式',
            default: 'fast',
            enum: ['fast', 'accurate'],
          },
        },
      })

      const res = parseConfigSchema(schema)
      expect(res.isValidJson).toBe(true)
      expect(res.isZeroConfig).toBe(false)
      expect(res.properties).toHaveLength(2)

      const thresholdProp = res.properties.find((p) => p.key === 'threshold')
      expect(thresholdProp).toBeDefined()
      expect(thresholdProp?.title).toBe('置信度阈值')
      expect(thresholdProp?.type).toBe('number')
      expect(thresholdProp?.required).toBe(true)
      expect(thresholdProp?.defaultValue).toBe('0.5')
      expect(thresholdProp?.range).toBe('0.1 ~ 0.9 (步长 0.05)')

      const modeProp = res.properties.find((p) => p.key === 'mode')
      expect(modeProp).toBeDefined()
      expect(modeProp?.required).toBe(false)
      expect(modeProp?.enumValues).toEqual(['fast', 'accurate'])
    })
  })

  describe('parseManifest', () => {
    it('handles empty input', () => {
      const res = parseManifest('')
      expect(res.isValidJson).toBe(true)
      expect(res.labels).toEqual([])
    })

    it('handles invalid json', () => {
      const res = parseManifest('broken')
      expect(res.isValidJson).toBe(false)
    })

    it('parses labels and resources', () => {
      const manifest = JSON.stringify({
        id: 'helmet_detector',
        labels: ['person', 'helmet', 'vest'],
        platform: {
          arch: 'x86_64',
          target: 'onnx',
        },
        resources: {
          compute_cost: 25,
          memory_mb: 128,
        },
      })

      const res = parseManifest(manifest)
      expect(res.isValidJson).toBe(true)
      expect(res.labels).toEqual(['person', 'helmet', 'vest'])
      expect(res.platformArch).toBe('x86_64')
      expect(res.platformTarget).toBe('onnx')
      expect(res.computeCost).toBe(25)
      expect(res.memoryMb).toBe(128)
    })

    it('supports classes fallback for labels', () => {
      const manifest = JSON.stringify({
        id: 'car_detector',
        classes: ['car', 'bus', 'truck'],
      })

      const res = parseManifest(manifest)
      expect(res.labels).toEqual(['car', 'bus', 'truck'])
    })
  })
})
