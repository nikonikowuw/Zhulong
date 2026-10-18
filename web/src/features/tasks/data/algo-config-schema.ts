import { z } from 'zod'

/**
 * 算法包 `config.schema.json` 是不可信输入（由第三方算法包提供），
 * 因此先用 Zod 收口再归一化为 UI 模型，绝不使用类型断言。
 */

/** JSON Schema 的 `type` 允许是字符串或字符串数组（Draft-07 的联合类型）。 */
const zTypeName = z.union([z.string(), z.array(z.string())])

const zRawProperty = z.looseObject({
  type: zTypeName.optional(),
  title: z.string().optional(),
  description: z.string().optional(),
  default: z.unknown(),
  minimum: z.number().optional(),
  maximum: z.number().optional(),
  step: z.number().optional(),
  enum: z.array(z.unknown()).optional(),
})

const zRawSchema = z.looseObject({
  $schema: z.string().optional(),
  type: zTypeName.optional(),
  properties: z.record(z.string(), z.unknown()).optional(),
  required: z.array(z.string()).optional(),
  additionalProperties: z.boolean().optional(),
})

const zJsonObject = z.record(z.string(), z.unknown())

/** 归一化后的字段定义，供 UI 做组件映射。 */
export interface SchemaProperty {
  type?: string
  title?: string
  description?: string
  default?: unknown
  minimum?: number
  maximum?: number
  step?: number
  enum?: (string | number)[]
}

export interface AlgoConfigSchema {
  $schema?: string
  type?: string
  properties: Record<string, SchemaProperty>
  required?: string[]
  additionalProperties?: boolean
}

export type AlgoConfigValues = Record<string, unknown>

function primaryType(type: string | string[] | undefined): string | undefined {
  if (Array.isArray(type)) return type.find((entry) => entry !== 'null')
  return type
}

function normalizeProperty(raw: z.infer<typeof zRawProperty>): SchemaProperty {
  const enumValues = (raw.enum ?? []).filter(
    (entry): entry is string | number =>
      typeof entry === 'string' || typeof entry === 'number'
  )
  return {
    type: primaryType(raw.type),
    title: raw.title,
    description: raw.description,
    default: raw.default,
    minimum: raw.minimum,
    maximum: raw.maximum,
    step: raw.step,
    enum: enumValues.length > 0 ? enumValues : undefined,
  }
}

/**
 * 解析算法包声明的 JSON Schema。单个字段损坏时只丢弃该字段，
 * 不会因为一个坏字段让整份 Schema 退化为零参。
 */
export function parseSchema(schemaJson?: string): AlgoConfigSchema | null {
  if (!schemaJson || schemaJson.trim() === '') return null

  let raw: unknown
  try {
    raw = JSON.parse(schemaJson)
  } catch {
    return null
  }
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null

  const envelope = zRawSchema.safeParse(raw)
  if (!envelope.success) return null

  const properties: Record<string, SchemaProperty> = {}
  for (const [key, rawProperty] of Object.entries(
    envelope.data.properties ?? {}
  )) {
    const parsed = zRawProperty.safeParse(rawProperty)
    if (!parsed.success) continue
    properties[key] = normalizeProperty(parsed.data)
  }

  return {
    $schema: envelope.data.$schema,
    type: primaryType(envelope.data.type),
    properties,
    required: envelope.data.required,
    additionalProperties: envelope.data.additionalProperties,
  }
}

/** 解析当前配置 JSON。返回 `null` 表示语法或结构非法，`{}` 表示空配置。 */
export function safeParseJson(raw: string): AlgoConfigValues | null {
  if (!raw || raw.trim() === '') return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (Array.isArray(parsed)) return null
  const result = zJsonObject.safeParse(parsed)
  return result.success ? result.data : null
}

export function extractDefaults(
  schema: AlgoConfigSchema | null
): AlgoConfigValues {
  const defaults: AlgoConfigValues = {}
  for (const [key, prop] of Object.entries(schema?.properties ?? {})) {
    if (prop.default !== undefined) defaults[key] = prop.default
  }
  return defaults
}

export function isZeroConfig(schema: AlgoConfigSchema | null): boolean {
  return Object.keys(schema?.properties ?? {}).length === 0
}

export function serializeConfig(values: AlgoConfigValues): string {
  return JSON.stringify(values, null, 2)
}

export function getDefaultConfigJson(schemaJson?: string): string {
  return serializeConfig(extractDefaults(parseSchema(schemaJson)))
}

/**
 * 把已保存的配置对齐到 Schema 声明的类型：字符串数字转数值、枚举回落到声明值。
 */
export function normalizeConfigValues(
  schema: AlgoConfigSchema | null,
  raw: AlgoConfigValues
): AlgoConfigValues {
  const next: AlgoConfigValues = { ...raw }
  for (const [key, prop] of Object.entries(schema?.properties ?? {})) {
    const value = next[key]
    if (value === undefined) continue

    if (
      (prop.type === 'number' || prop.type === 'integer') &&
      typeof value === 'string'
    ) {
      const parsed =
        prop.type === 'integer'
          ? Number.parseInt(value, 10)
          : Number.parseFloat(value)
      if (!Number.isNaN(parsed)) next[key] = parsed
    }

    if (prop.enum && prop.enum.length > 0) {
      const matched = prop.enum.find(
        (option) => String(option) === String(value)
      )
      if (matched !== undefined) next[key] = matched
    }
  }
  return next
}

/** 默认值 + 当前值，作为表单初始值。 */
export function buildFormValues(
  schema: AlgoConfigSchema | null,
  raw: AlgoConfigValues
): AlgoConfigValues {
  return { ...extractDefaults(schema), ...normalizeConfigValues(schema, raw) }
}

function buildFieldSchema(prop: SchemaProperty): z.ZodType {
  if (prop.enum && prop.enum.length > 0) {
    const [first, ...rest] = prop.enum
    return rest.reduce<z.ZodType>(
      (acc, option) => acc.or(z.literal(option)),
      z.literal(first)
    )
  }
  if (prop.type === 'boolean') return z.boolean()
  if (prop.type === 'number' || prop.type === 'integer') {
    let num = z.number()
    if (prop.type === 'integer') num = num.int()
    if (prop.minimum !== undefined) num = num.min(prop.minimum)
    if (prop.maximum !== undefined) num = num.max(prop.maximum)
    return num
  }
  if (prop.type === 'string') return z.string()
  return z.unknown()
}

/** 由算法 Schema 派生的表单校验契约（宽松对象，保留算法自定义的额外键）。 */
export function buildConfigFormSchema(schema: AlgoConfigSchema | null) {
  const shape: Record<string, z.ZodType> = {}
  const required = new Set(schema?.required ?? [])
  for (const [key, prop] of Object.entries(schema?.properties ?? {})) {
    const field = buildFieldSchema(prop)
    shape[key] = required.has(key) ? field : field.optional()
  }
  return z.looseObject(shape)
}

export function isNumericProperty(prop: SchemaProperty): boolean {
  return prop.type === 'number' || prop.type === 'integer'
}

export function isBoundedProperty(prop: SchemaProperty): boolean {
  return (
    isNumericProperty(prop) &&
    prop.minimum !== undefined &&
    prop.maximum !== undefined
  )
}

/** 数值字段步长：显式 `step` 优先；无边界浮点返回 `any`，不再退化为整数步长。 */
export function numericStep(prop: SchemaProperty): number | 'any' {
  if (prop.step !== undefined) return prop.step
  if (prop.type === 'integer') return 1
  if (prop.minimum === undefined || prop.maximum === undefined) return 'any'
  const range = prop.maximum - prop.minimum
  if (range <= 1) return 0.01
  if (range <= 10) return 0.1
  return 1
}

/** 下拉框回填时把字符串还原为枚举声明的原始类型（支持混合枚举）。 */
export function coerceEnumOption(
  prop: SchemaProperty,
  selected: string
): string | number {
  const matched = prop.enum?.find((option) => String(option) === selected)
  return matched === undefined ? selected : matched
}
