import { describe, expect, it } from 'vitest'
import {
  buildConfigFormSchema,
  buildFormValues,
  coerceEnumOption,
  extractDefaults,
  getDefaultConfigJson,
  isBoundedProperty,
  isZeroConfig,
  normalizeConfigValues,
  numericStep,
  parseSchema,
  safeParseJson,
  serializeConfig,
} from './algo-config-schema'

const schemaJson = JSON.stringify({
  $schema: 'http://json-schema.org/draft-07/schema#',
  type: 'object',
  required: ['threshold'],
  properties: {
    threshold: {
      type: 'number',
      title: '比对阈值',
      minimum: 0,
      maximum: 1,
      step: 0.05,
      default: 0.4,
    },
    top_k: { type: 'integer', title: 'Top K', minimum: 1, maximum: 20 },
    save_snapshot: { type: 'boolean', default: true },
    model_precision: { type: 'string', enum: ['fp32', 'fp16'] },
    mixed_mode: { enum: ['auto', 1] },
    prefix: { type: 'string', default: 'face_' },
  },
})

describe('parseSchema', () => {
  it('rejects missing, blank, malformed and non-object input', () => {
    expect(parseSchema()).toBeNull()
    expect(parseSchema('')).toBeNull()
    expect(parseSchema('   ')).toBeNull()
    expect(parseSchema('not json')).toBeNull()
    expect(parseSchema('[]')).toBeNull()
    expect(parseSchema('null')).toBeNull()
    expect(parseSchema('42')).toBeNull()
  })

  it('accepts an empty object as a valid zero-config schema', () => {
    const schema = parseSchema('{}')
    expect(schema).not.toBeNull()
    expect(isZeroConfig(schema)).toBe(true)
  })

  it('normalizes declared field types and keywords', () => {
    const schema = parseSchema(schemaJson)
    expect(schema?.required).toEqual(['threshold'])
    expect(schema?.properties.threshold).toMatchObject({
      type: 'number',
      title: '比对阈值',
      minimum: 0,
      maximum: 1,
      step: 0.05,
      default: 0.4,
    })
    expect(schema?.properties.mixed_mode?.enum).toEqual(['auto', 1])
    expect(isZeroConfig(schema)).toBe(false)
  })

  it('keeps a good field when a sibling field is malformed', () => {
    const schema = parseSchema(
      JSON.stringify({
        properties: {
          good: { type: 'string' },
          bad: { type: 'number', minimum: 'not-a-number' },
        },
      })
    )
    expect(Object.keys(schema?.properties ?? {})).toEqual(['good'])
  })

  it('drops enum members that are neither string nor number', () => {
    const schema = parseSchema(
      JSON.stringify({
        properties: { mode: { enum: ['a', null, { x: 1 }, 2] } },
      })
    )
    expect(schema?.properties.mode?.enum).toEqual(['a', 2])
  })

  it('resolves a type union to its first non-null member', () => {
    const schema = parseSchema(
      JSON.stringify({ properties: { level: { type: ['null', 'integer'] } } })
    )
    expect(schema?.properties.level?.type).toBe('integer')
  })
})

describe('safeParseJson', () => {
  it('treats blank input as an empty object', () => {
    expect(safeParseJson('')).toEqual({})
    expect(safeParseJson('  ')).toEqual({})
  })

  it('returns null for invalid JSON and non-object documents', () => {
    expect(safeParseJson('{ invalid }')).toBeNull()
    expect(safeParseJson('[1,2]')).toBeNull()
    expect(safeParseJson('"text"')).toBeNull()
  })

  it('returns parsed objects', () => {
    expect(safeParseJson('{"a":1}')).toEqual({ a: 1 })
  })
})

describe('defaults', () => {
  it('extracts only declared defaults', () => {
    expect(extractDefaults(parseSchema(schemaJson))).toEqual({
      threshold: 0.4,
      save_snapshot: true,
      prefix: 'face_',
    })
  })

  it('serializes defaults and falls back to an empty object', () => {
    expect(getDefaultConfigJson()).toBe('{}')
    expect(getDefaultConfigJson('{}')).toBe('{}')
    expect(getDefaultConfigJson('invalid')).toBe('{}')
    expect(JSON.parse(getDefaultConfigJson(schemaJson))).toEqual({
      threshold: 0.4,
      save_snapshot: true,
      prefix: 'face_',
    })
  })

  it('seeds defaults first and lets stored values win', () => {
    expect(
      buildFormValues(parseSchema(schemaJson), { threshold: 0.9 })
    ).toEqual({
      threshold: 0.9,
      save_snapshot: true,
      prefix: 'face_',
    })
  })
})

describe('normalizeConfigValues', () => {
  it('coerces string-encoded numbers to numbers', () => {
    const schema = parseSchema(schemaJson)
    expect(
      normalizeConfigValues(schema, { threshold: '0.45', top_k: '5' })
    ).toEqual({ threshold: 0.45, top_k: 5 })
  })

  it('restores enum members to their declared type', () => {
    const schema = parseSchema(schemaJson)
    expect(normalizeConfigValues(schema, { mixed_mode: '1' })).toEqual({
      mixed_mode: 1,
    })
  })

  it('leaves unrelated keys untouched', () => {
    const schema = parseSchema(schemaJson)
    expect(normalizeConfigValues(schema, { custom: 'kept' })).toEqual({
      custom: 'kept',
    })
  })
})

describe('numericStep', () => {
  it('prefers an explicit step', () => {
    expect(numericStep({ type: 'number', step: 0.05 })).toBe(0.05)
  })

  it('uses an integer step for integer fields', () => {
    expect(numericStep({ type: 'integer' })).toBe(1)
  })

  it('allows arbitrary precision for unbounded floats', () => {
    expect(numericStep({ type: 'number' })).toBe('any')
  })

  it('derives a float step from a bounded range', () => {
    expect(numericStep({ type: 'number', minimum: 0, maximum: 1 })).toBe(0.01)
    expect(numericStep({ type: 'number', minimum: 0, maximum: 8 })).toBe(0.1)
    expect(numericStep({ type: 'number', minimum: 0, maximum: 100 })).toBe(1)
  })
})

describe('isBoundedProperty', () => {
  it('requires both bounds on a numeric field', () => {
    expect(isBoundedProperty({ type: 'number', minimum: 0, maximum: 1 })).toBe(
      true
    )
    expect(isBoundedProperty({ type: 'number', minimum: 0 })).toBe(false)
    expect(isBoundedProperty({ type: 'string', minimum: 0, maximum: 1 })).toBe(
      false
    )
  })
})

describe('coerceEnumOption', () => {
  it('round-trips a numeric enum member', () => {
    expect(coerceEnumOption({ enum: ['fp32', 'fp16'] }, 'fp16')).toBe('fp16')
  })

  it('resolves a mixed enum member back to its declared type', () => {
    expect(coerceEnumOption({ enum: ['auto', 1] }, '1')).toBe(1)
    expect(coerceEnumOption({ enum: ['auto', 1] }, 'auto')).toBe('auto')
  })

  it('falls back to the raw string for an unknown option', () => {
    expect(coerceEnumOption({ enum: ['a'] }, 'zzz')).toBe('zzz')
  })
})

describe('buildConfigFormSchema', () => {
  it('accepts a valid config and preserves unknown keys', () => {
    const schema = buildConfigFormSchema(parseSchema(schemaJson))
    const result = schema.safeParse({
      threshold: 0.5,
      save_snapshot: true,
      custom: 'kept',
    })
    expect(result.success).toBe(true)
    expect(result.data).toMatchObject({ custom: 'kept' })
  })

  it('rejects an out-of-range or wrongly typed value', () => {
    const schema = buildConfigFormSchema(parseSchema(schemaJson))
    expect(schema.safeParse({ threshold: 5 }).success).toBe(false)
    expect(schema.safeParse({ threshold: 'abc' }).success).toBe(false)
    expect(schema.safeParse({ save_snapshot: 'yes' }).success).toBe(false)
  })

  it('rejects a missing required field and allows a missing optional one', () => {
    const schema = buildConfigFormSchema(parseSchema(schemaJson))
    expect(schema.safeParse({}).success).toBe(false)
    expect(schema.safeParse({ threshold: 0.5 }).success).toBe(true)
  })

  it('accepts undefined for an optional field so it can be cleared', () => {
    const schema = buildConfigFormSchema(parseSchema(schemaJson))
    const result = schema.safeParse({ threshold: 0.5, top_k: undefined })
    expect(result.success).toBe(true)
  })

  it('builds an empty schema for a zero-config algorithm', () => {
    expect(buildConfigFormSchema(null).safeParse({}).success).toBe(true)
  })
})

describe('serializeConfig', () => {
  it('omits undefined keys instead of emitting null', () => {
    expect(serializeConfig({ a: 1, b: undefined })).toBe('{\n  "a": 1\n}')
  })
})
