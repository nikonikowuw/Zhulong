import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  buildConfigFormSchema,
  buildFormValues,
  extractDefaults,
  isZeroConfig,
  parseSchema,
  safeParseJson,
  serializeConfig,
  type AlgoConfigValues,
} from '../data/algo-config-schema'

export type AlgoConfigMode = 'form' | 'json'

interface UseAlgoConfigFormOptions {
  schemaJson?: string
  value: string
  onChange: (nextJson: string) => void
}

/**
 * 算法配置双模态（表单 ⇄ JSON 源码）的全部状态与派生逻辑。
 * 组件只负责渲染，规则铁律 3。
 */
export function useAlgoConfigForm({
  schemaJson,
  value,
  onChange,
}: UseAlgoConfigFormOptions) {
  const { t } = useTranslation('tasks')

  const schema = useMemo(() => parseSchema(schemaJson), [schemaJson])
  const propertyEntries = useMemo(
    () => Object.entries(schema?.properties ?? {}),
    [schema]
  )
  const defaults = useMemo(() => extractDefaults(schema), [schema])
  const zeroConfig = isZeroConfig(schema)
  const formSchema = useMemo(() => buildConfigFormSchema(schema), [schema])

  // 仅取挂载时的配置作为表单初始值；后续以 value 为唯一真实源。
  const [initialValues] = useState(() =>
    buildFormValues(schema, safeParseJson(value) ?? {})
  )

  const form = useForm<AlgoConfigValues>({
    resolver: zodResolver(formSchema),
    defaultValues: initialValues,
    mode: 'onChange',
  })

  const [mode, setMode] = useState<AlgoConfigMode>('form')
  const [copied, setCopied] = useState(false)
  const copyTimer = useRef<number | null>(null)

  useEffect(
    () => () => {
      if (copyTimer.current !== null) window.clearTimeout(copyTimer.current)
    },
    []
  )

  const parsedValue = useMemo(() => safeParseJson(value), [value])
  const isValidJson = parsedValue !== null

  const commit = useCallback(
    (next: AlgoConfigValues) => onChange(serializeConfig(next)),
    [onChange]
  )

  const setFieldValue = useCallback(
    (key: string, next: unknown) => {
      form.setValue(key, next, { shouldValidate: true, shouldDirty: true })
      commit({ ...form.getValues(), [key]: next })
    },
    [commit, form]
  )

  const switchMode = useCallback(
    (target: AlgoConfigMode) => {
      if (target === 'json') {
        setMode('json')
        return
      }
      const parsed = safeParseJson(value)
      if (parsed === null) {
        toast.error(
          t('dynamicForm.jsonSyntaxError', 'JSON 格式错误，无法切换至表单模式')
        )
        return
      }
      form.reset(buildFormValues(schema, parsed))
      setMode('form')
    },
    [form, schema, t, value]
  )

  const resetDefaults = useCallback(() => {
    const seeded = buildFormValues(schema, {})
    form.reset(seeded)
    commit(seeded)
    toast.success(t('dynamicForm.resetSuccess', '已恢复至算法默认参数'))
  }, [commit, form, schema, t])

  const formatJson = useCallback(() => {
    const parsed = safeParseJson(value)
    if (parsed === null) {
      toast.error(t('dynamicForm.formatError', 'JSON 语法错误，无法格式化'))
      return
    }
    commit(parsed)
    toast.success(t('dynamicForm.formatSuccess', 'JSON 格式化成功'))
  }, [commit, t, value])

  const copyJson = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value || '{}')
    } catch {
      toast.error(
        t('dynamicForm.copyError', '复制失败，请检查浏览器剪贴板权限')
      )
      return
    }
    setCopied(true)
    if (copyTimer.current !== null) window.clearTimeout(copyTimer.current)
    copyTimer.current = window.setTimeout(() => setCopied(false), 2000)
    toast.success(t('dynamicForm.copied', '已复制'))
  }, [t, value])

  return {
    form,
    schema,
    propertyEntries,
    defaults,
    zeroConfig,
    mode,
    switchMode,
    setFieldValue,
    resetDefaults,
    value,
    isValidJson,
    formatJson,
    copyJson,
    copied,
  }
}
