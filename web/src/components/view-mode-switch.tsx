import { type LucideIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'

export interface ViewModeOption<TView extends string> {
  value: TView
  /** 可见文案，同时作为 `title` 与 `aria-label`（窄屏下文本被 `hidden sm:inline` 收起） */
  label: string
  icon: LucideIcon
}

interface ViewModeSwitchProps<TView extends string> {
  value: TView
  /** 首项为页面的主视角，默认态恒高亮在最左侧 */
  options: ReadonlyArray<ViewModeOption<TView>>
  onChange: (value: TView) => void
}

export function ViewModeSwitch<TView extends string>({
  value,
  options,
  onChange,
}: ViewModeSwitchProps<TView>): React.JSX.Element {
  return (
    <div className='flex items-center gap-1 rounded-lg border border-border bg-muted/40 p-0.5'>
      {options.map((option) => {
        const Icon = option.icon
        const isActive = option.value === value

        return (
          <Button
            key={option.value}
            type='button'
            variant={isActive ? 'secondary' : 'ghost'}
            size='sm'
            aria-pressed={isActive}
            aria-label={option.label}
            title={option.label}
            onClick={() => onChange(option.value)}
            className='h-7 gap-1.5 px-2 text-xs'
          >
            <Icon className='h-3.5 w-3.5' />
            <span className='hidden sm:inline'>{option.label}</span>
          </Button>
        )
      })}
    </div>
  )
}
