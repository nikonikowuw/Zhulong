import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { AlgoConfigDynamicForm } from './algo-config-dynamic-form'

const schemaJson = JSON.stringify({
  $schema: 'http://json-schema.org/draft-07/schema#',
  type: 'object',
  properties: {
    threshold: {
      type: 'number',
      title: '比对阈值',
      description: '人脸比对余弦相似度命中阈值',
      minimum: 0,
      maximum: 1,
      step: 0.05,
      default: 0.4,
    },
    top_k: {
      type: 'integer',
      title: 'Top K 上限',
      minimum: 1,
      maximum: 20,
      default: 3,
    },
    retry: { type: 'number', title: '重试次数' },
    save_snapshot: { type: 'boolean', title: '抓拍留存', default: true },
    model_precision: {
      type: 'string',
      title: '推理精度',
      enum: ['fp32', 'fp16', 'int8'],
      default: 'fp16',
    },
    mixed_mode: { title: '混合枚举', enum: ['auto', 1] },
    prefix: { type: 'string', title: '抓拍前缀', default: 'face_' },
    nested: { type: 'object', title: '嵌套结构' },
  },
})

/** 受控宿主：让「表单 → JSON → 表单」的真实闭环可被断言。 */
function Harness({
  initialValue,
  onChange,
  schema = schemaJson,
}: {
  initialValue: string
  onChange?: (next: string) => void
  schema?: string
}) {
  const [value, setValue] = useState(initialValue)
  return (
    <AlgoConfigDynamicForm
      schemaJson={schema}
      value={value}
      onChange={(next) => {
        setValue(next)
        onChange?.(next)
      }}
    />
  )
}

function lastCommit(onChange: ReturnType<typeof vi.fn>): string {
  const calls = onChange.mock.calls
  expect(calls.length).toBeGreaterThan(0)
  return calls[calls.length - 1][0] as string
}

describe('AlgoConfigDynamicForm · 零参算法', () => {
  it('shows the zero-config notice and no raw JSON box for an empty schema', async () => {
    const { getByText, getByPlaceholder } = await render(
      <AlgoConfigDynamicForm schemaJson='{}' value='{}' onChange={vi.fn()} />
    )

    await expect
      .element(getByText('该算法无需额外参数配置（零参算法）'))
      .toBeInTheDocument()
    expect(getByPlaceholder('{}').query()).toBeNull()
  })

  it('shows the zero-config notice when the algorithm declares no schema', async () => {
    const { getByText, getByPlaceholder } = await render(
      <AlgoConfigDynamicForm value='{}' onChange={vi.fn()} />
    )

    await expect
      .element(getByText('该算法无需额外参数配置（零参算法）'))
      .toBeInTheDocument()
    expect(getByPlaceholder('{}').query()).toBeNull()
  })

  it('still lets the operator reach the raw JSON editor', async () => {
    const { getByText, getByPlaceholder } = await render(
      <AlgoConfigDynamicForm schemaJson='{}' value='{}' onChange={vi.fn()} />
    )

    await getByText('JSON 源码').click()
    await expect.element(getByPlaceholder('{}')).toBeInTheDocument()
  })
})

describe('AlgoConfigDynamicForm · 字段映射', () => {
  it('maps each schema type onto the documented control', async () => {
    const { getByRole, getByText } = await render(<Harness initialValue='{}' />)

    // 有界数值 → Slider + 数字框；无界数值 → 数字框
    await expect
      .element(getByRole('slider', { name: '比对阈值' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('slider', { name: 'Top K 上限' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('textbox', { name: '重试次数' }))
      .toBeInTheDocument()
    // 布尔 → Switch，枚举 → Select，字符串 → Input
    await expect
      .element(getByRole('switch', { name: '抓拍留存' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('combobox', { name: '推理精度' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('combobox', { name: '混合枚举' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('textbox', { name: '抓拍前缀' }))
      .toBeInTheDocument()

    await expect.element(getByText('8 项')).toBeInTheDocument()
  })

  it('renders an unsupported hint for complex structures', async () => {
    const { getByText } = await render(<Harness initialValue='{}' />)
    await expect
      .element(getByText('复杂数据结构（请切换至 JSON 模式编辑）'))
      .toBeInTheDocument()
  })

  it('seeds declared defaults for an unconfigured algorithm', async () => {
    const { getByRole } = await render(<Harness initialValue='{}' />)

    await expect
      .element(getByRole('textbox', { name: '比对阈值' }))
      .toHaveValue('0.4')
    await expect
      .element(getByRole('textbox', { name: '抓拍前缀' }))
      .toHaveValue('face_')
    await expect
      .element(getByRole('switch', { name: '抓拍留存' }))
      .toBeChecked()
  })

  it('normalizes string-encoded numbers from a stored config', async () => {
    const { getByRole } = await render(
      <Harness initialValue='{"threshold":"0.85","top_k":"7"}' />
    )

    await expect
      .element(getByRole('textbox', { name: '比对阈值' }))
      .toHaveValue('0.85')
    await expect
      .element(getByRole('textbox', { name: 'Top K 上限' }))
      .toHaveValue('7')
  })
})

describe('AlgoConfigDynamicForm · 数值字段', () => {
  it('commits a decimal value typed into a bounded field', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"threshold":0.4}' onChange={onChange} />
    )

    const input = getByRole('textbox', { name: '比对阈值' })
    await input.fill('0.85')
    await userEvent.keyboard('{Enter}')

    expect(JSON.parse(lastCommit(onChange)).threshold).toBe(0.85)
  })

  it('clamps a cleared bounded field to its minimum', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"threshold":0.4}' onChange={onChange} />
    )

    const input = getByRole('textbox', { name: '比对阈值' })
    await input.fill('')
    await userEvent.keyboard('{Enter}')

    expect(JSON.parse(lastCommit(onChange)).threshold).toBe(0)
  })

  it('drops the key instead of writing null when an unbounded field is cleared', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"retry":3}' onChange={onChange} />
    )

    const input = getByRole('textbox', { name: '重试次数' })
    await input.fill('')
    await userEvent.keyboard('{Enter}')

    const committed = JSON.parse(lastCommit(onChange))
    expect('retry' in committed).toBe(false)
    // 其余字段回落到 Schema 默认值，且没有 null 写入
    expect(committed).toMatchObject({ threshold: 0.4, save_snapshot: true })
    expect(Object.values(committed)).not.toContain(null)
  })

  it('clamps an out-of-range typed value instead of committing it', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"threshold":0.4}' onChange={onChange} />
    )

    const input = getByRole('textbox', { name: '比对阈值' })
    await input.fill('9')
    await userEvent.keyboard('{Enter}')

    expect(JSON.parse(lastCommit(onChange)).threshold).toBe(1)
    await expect.element(input).toHaveValue('1')
  })

  it('moves the slider for a bounded field and keeps the number box in sync', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"threshold":0.4}' onChange={onChange} />
    )

    const slider = getByRole('slider', { name: '比对阈值' })
    ;(slider.element() as HTMLElement).focus()
    await userEvent.keyboard('{ArrowRight}')

    const committed = JSON.parse(lastCommit(onChange)).threshold
    expect(committed).toBeGreaterThan(0.4)
    await expect
      .element(getByRole('textbox', { name: '比对阈值' }))
      .toHaveValue(String(committed))
  })
})

describe('AlgoConfigDynamicForm · 双模同步', () => {
  it('serializes a form edit into the raw JSON view', async () => {
    const { getByText, getByRole, getByPlaceholder } = await render(
      <Harness initialValue='{"save_snapshot":true}' />
    )

    await getByRole('switch', { name: '抓拍留存' }).click()
    await getByText('JSON 源码').click()

    const textarea = getByPlaceholder('{}')
    await expect.element(textarea).toBeInTheDocument()
    const raw = (textarea.element() as HTMLTextAreaElement).value
    expect(JSON.parse(raw).save_snapshot).toBe(false)
  })

  it('reflects raw JSON edits back into the form when switching back', async () => {
    const { getByText, getByRole, getByPlaceholder } = await render(
      <Harness initialValue='{"threshold":0.4}' />
    )

    await getByText('JSON 源码').click()
    await getByPlaceholder('{}').fill('{"threshold":0.9}')
    await getByText('表单模式').click()

    await expect
      .element(getByRole('textbox', { name: '比对阈值' }))
      .toHaveValue('0.9')
  })

  it('blocks the switch back to form mode when the raw JSON is invalid', async () => {
    const { getByText, getByPlaceholder } = await render(
      <Harness initialValue='{"threshold":0.4}' />
    )

    await getByText('JSON 源码').click()
    await getByPlaceholder('{}').fill('{ invalid json }')
    await getByText('表单模式').click()

    // 仍停留在 JSON 模式，表单字段未被渲染
    await expect.element(getByPlaceholder('{}')).toBeInTheDocument()
    expect(getByText('比对阈值').query()).toBeNull()
  })

  it('formats the raw JSON on demand', async () => {
    const { getByText, getByPlaceholder } = await render(
      <Harness initialValue='{"threshold":0.4,"top_k":3}' />
    )

    await getByText('JSON 源码').click()
    await getByPlaceholder('{}').fill('{"threshold":0.4,"top_k":3}')
    await getByText('格式化').click()

    const raw = (getByPlaceholder('{}').element() as HTMLTextAreaElement).value
    expect(raw).toBe('{\n  "threshold": 0.4,\n  "top_k": 3\n}')
  })

  it('marks the JSON view as invalid while the text is malformed', async () => {
    const { getByText, getByPlaceholder } = await render(
      <Harness initialValue='{"threshold":0.4}' />
    )

    await getByText('JSON 源码').click()
    await expect.element(getByText('格式正确')).toBeInTheDocument()

    await getByPlaceholder('{}').fill('{ oops }')
    await expect.element(getByText('语法错误')).toBeInTheDocument()
  })
})

describe('AlgoConfigDynamicForm · 恢复默认值', () => {
  it('resets every field to the schema default', async () => {
    const onChange = vi.fn()
    const { getByText } = await render(
      <Harness
        initialValue='{"threshold":0.99,"top_k":20,"save_snapshot":false}'
        onChange={onChange}
      />
    )

    await getByText('恢复默认值').click()

    expect(JSON.parse(lastCommit(onChange))).toMatchObject({
      threshold: 0.4,
      top_k: 3,
      save_snapshot: true,
      model_precision: 'fp16',
      prefix: 'face_',
    })
  })

  it('hides the reset action for a zero-config algorithm', async () => {
    const { getByText } = await render(
      <AlgoConfigDynamicForm schemaJson='{}' value='{}' onChange={vi.fn()} />
    )
    expect(getByText('恢复默认值').query()).toBeNull()
  })
})

describe('AlgoConfigDynamicForm · 枚举', () => {
  it('round-trips a numeric enum member as a number', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{}' onChange={onChange} />
    )

    await getByRole('combobox', { name: '混合枚举' }).click()
    await getByRole('option', { name: '1' }).click()

    expect(JSON.parse(lastCommit(onChange)).mixed_mode).toBe(1)
  })

  it('commits a string enum member unchanged', async () => {
    const onChange = vi.fn()
    const { getByRole } = await render(
      <Harness initialValue='{"model_precision":"fp16"}' onChange={onChange} />
    )

    await getByRole('combobox', { name: '推理精度' }).click()
    await getByRole('option', { name: 'int8' }).click()

    expect(JSON.parse(lastCommit(onChange)).model_precision).toBe('int8')
  })
})
