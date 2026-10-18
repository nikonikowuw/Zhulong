import { type Control } from 'react-hook-form'
import { AlertCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { FormControl, FormField, FormItem } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  coerceEnumOption,
  isNumericProperty,
  type AlgoConfigValues,
  type SchemaProperty,
} from '../data/algo-config-schema'
import { AlgoConfigFieldLabel } from './algo-config-field-label'
import { AlgoConfigNumberField } from './algo-config-number-field'

interface AlgoConfigFieldProps {
  control: Control<AlgoConfigValues>
  propertyKey: string
  prop: SchemaProperty
  disabled: boolean
  onValueChange: (key: string, value: unknown) => void
}

function toNumber(value: unknown): number | undefined {
  if (typeof value === 'number') return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isNaN(parsed) ? undefined : parsed
  }
  return undefined
}

/**
 * 单个 Schema 属性的控件映射（枚举 → Select，布尔 → Switch，
 * 数值 → Slider/数字框，字符串 → Input，其余降级提示 JSON 模式）。
 */
export function AlgoConfigField({
  control,
  propertyKey,
  prop,
  disabled,
  onValueChange,
}: AlgoConfigFieldProps) {
  const { t } = useTranslation('tasks')
  const title = prop.title || propertyKey
  const hasEnum = Boolean(prop.enum && prop.enum.length > 0)

  return (
    <FormField
      control={control}
      name={propertyKey}
      render={({ field }) => (
        <FormItem className='flex items-center justify-between gap-3 p-2.5 transition-colors hover:bg-muted/20'>
          <AlgoConfigFieldLabel
            title={title}
            propertyKey={propertyKey}
            description={prop.description}
          />

          <div className='flex shrink-0 items-center gap-2'>
            {hasEnum && prop.enum ? (
              <Select
                disabled={disabled}
                value={field.value === undefined ? '' : String(field.value)}
                onValueChange={(selected) =>
                  onValueChange(propertyKey, coerceEnumOption(prop, selected))
                }
              >
                <FormControl>
                  <SelectTrigger
                    size='sm'
                    className='w-32 bg-background font-mono'
                  >
                    <SelectValue
                      placeholder={t(
                        'dynamicForm.selectPlaceholder',
                        '请选择...'
                      )}
                    />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  {prop.enum.map((option) => (
                    <SelectItem
                      key={String(option)}
                      value={String(option)}
                      className='font-mono text-xs'
                    >
                      {String(option)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : prop.type === 'boolean' ? (
              <>
                <span className='font-mono text-[11px] text-muted-foreground'>
                  {field.value
                    ? t('dynamicForm.trueLabel', '开启')
                    : t('dynamicForm.falseLabel', '关闭')}
                </span>
                <FormControl>
                  <Switch
                    disabled={disabled}
                    checked={Boolean(field.value)}
                    onCheckedChange={(checked) =>
                      onValueChange(propertyKey, checked)
                    }
                  />
                </FormControl>
              </>
            ) : isNumericProperty(prop) ? (
              <AlgoConfigNumberField
                prop={prop}
                label={title}
                value={toNumber(field.value)}
                disabled={disabled}
                onValueCommit={(next) => onValueChange(propertyKey, next)}
              />
            ) : prop.type === 'string' ? (
              <FormControl>
                <Input
                  disabled={disabled}
                  value={typeof field.value === 'string' ? field.value : ''}
                  onChange={(event) =>
                    onValueChange(propertyKey, event.target.value)
                  }
                  className='h-7 w-36 bg-background font-mono text-xs'
                />
              </FormControl>
            ) : (
              <Badge
                variant='outline'
                className='gap-1.5 text-[10px] font-normal'
              >
                <AlertCircle className='h-3 w-3' />
                {t(
                  'dynamicForm.unsupportedField',
                  '复杂数据结构（请切换至 JSON 模式编辑）'
                )}
              </Badge>
            )}
          </div>
        </FormItem>
      )}
    />
  )
}
