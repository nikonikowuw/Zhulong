import { z } from 'zod'

/**
 * 字段 Schema 原始属性定义
 */
const rawPropertySchema = z.looseObject({
  type: z.union([z.string(), z.array(z.string())]).optional(),
  title: z.string().optional(),
  description: z.string().optional(),
  default: z.unknown(),
  minimum: z.number().optional(),
  maximum: z.number().optional(),
  step: z.number().optional(),
  enum: z.array(z.unknown()).optional(),
})

const rawConfigSchema = z.looseObject({
  $schema: z.string().optional(),
  type: z.union([z.string(), z.array(z.string())]).optional(),
  properties: z.record(z.string(), z.unknown()).optional(),
  required: z.array(z.string()).optional(),
})

export interface ParsedConfigProperty {
  key: string
  title?: string
  type: string
  description?: string
  defaultValue?: string
  range?: string
  enumValues?: string[]
  required: boolean
}

export interface ParsedConfigSchemaResult {
  isZeroConfig: boolean
  isValidJson: boolean
  rawFormatted: string
  properties: ParsedConfigProperty[]
}

/**
 * 解析 config.schema.json
 */
export function parseConfigSchema(
  schemaJson?: string
): ParsedConfigSchemaResult {
  if (!schemaJson || schemaJson.trim() === '') {
    return {
      isZeroConfig: true,
      isValidJson: true,
      rawFormatted: '{}',
      properties: [],
    }
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(schemaJson)
  } catch {
    return {
      isZeroConfig: true,
      isValidJson: false,
      rawFormatted: schemaJson,
      properties: [],
    }
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return {
      isZeroConfig: true,
      isValidJson: true,
      rawFormatted: JSON.stringify(parsed, null, 2),
      properties: [],
    }
  }

  const envelope = rawConfigSchema.safeParse(parsed)
  if (!envelope.success) {
    return {
      isZeroConfig: true,
      isValidJson: true,
      rawFormatted: JSON.stringify(parsed, null, 2),
      properties: [],
    }
  }

  const requiredSet = new Set(envelope.data.required ?? [])
  const properties: ParsedConfigProperty[] = []

  for (const [key, rawProp] of Object.entries(envelope.data.properties ?? {})) {
    const propRes = rawPropertySchema.safeParse(rawProp)
    if (!propRes.success) continue
    const data = propRes.data

    let typeStr = 'unknown'
    if (typeof data.type === 'string') {
      typeStr = data.type
    } else if (Array.isArray(data.type)) {
      typeStr = data.type.filter((t) => t !== 'null').join(' | ') || 'unknown'
    }

    let range: string | undefined
    if (data.minimum !== undefined && data.maximum !== undefined) {
      range = `${data.minimum} ~ ${data.maximum}`
      if (data.step !== undefined) range += ` (步长 ${data.step})`
    } else if (data.minimum !== undefined) {
      range = `≥ ${data.minimum}`
    } else if (data.maximum !== undefined) {
      range = `≤ ${data.maximum}`
    }

    let enumValues: string[] | undefined
    if (data.enum && data.enum.length > 0) {
      enumValues = data.enum.map((v) => String(v))
    }

    let defaultValue: string | undefined
    if (data.default !== undefined) {
      defaultValue =
        typeof data.default === 'object'
          ? JSON.stringify(data.default)
          : String(data.default)
    }

    properties.push({
      key,
      title: data.title,
      type: typeStr,
      description: data.description,
      defaultValue,
      range,
      enumValues,
      required: requiredSet.has(key),
    })
  }

  return {
    isZeroConfig: properties.length === 0,
    isValidJson: true,
    rawFormatted: JSON.stringify(parsed, null, 2),
    properties,
  }
}

const rawManifestSchema = z.looseObject({
  id: z.string().optional(),
  name: z.string().optional(),
  version: z.string().optional(),
  description: z.string().optional(),
  task_type: z.string().optional(),
  category: z.string().optional(),
  labels: z.array(z.string()).optional(),
  classes: z.array(z.string()).optional(),
  platform: z
    .looseObject({
      arch: z.string().optional(),
      target: z.string().optional(),
    })
    .optional(),
  resources: z
    .looseObject({
      compute_cost: z.number().optional(),
      memory_mb: z.number().optional(),
    })
    .optional(),
})

export interface ParsedManifestResult {
  isValidJson: boolean
  rawFormatted: string
  labels: string[]
  platformArch?: string
  platformTarget?: string
  computeCost?: number
  memoryMb?: number
  rawObject?: Record<string, unknown>
}

/**
 * 解析 manifest.json
 */
export function parseManifest(manifestJson?: string): ParsedManifestResult {
  if (!manifestJson || manifestJson.trim() === '') {
    return {
      isValidJson: true,
      rawFormatted: '{}',
      labels: [],
    }
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(manifestJson)
  } catch {
    return {
      isValidJson: false,
      rawFormatted: manifestJson,
      labels: [],
    }
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return {
      isValidJson: true,
      rawFormatted: JSON.stringify(parsed, null, 2),
      labels: [],
    }
  }

  const envelope = rawManifestSchema.safeParse(parsed)
  const labels: string[] = []
  if (envelope.success) {
    if (envelope.data.labels) labels.push(...envelope.data.labels)
    else if (envelope.data.classes) labels.push(...envelope.data.classes)
  }

  return {
    isValidJson: true,
    rawFormatted: JSON.stringify(parsed, null, 2),
    labels,
    platformArch: envelope.success ? envelope.data.platform?.arch : undefined,
    platformTarget: envelope.success
      ? envelope.data.platform?.target
      : undefined,
    computeCost: envelope.success
      ? envelope.data.resources?.compute_cost
      : undefined,
    memoryMb: envelope.success ? envelope.data.resources?.memory_mb : undefined,
    rawObject: parsed as Record<string, unknown>,
  }
}
