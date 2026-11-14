import { memo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'

export interface SelectableCardWrapperProps<T extends { id: string }> {
  item: T
  selected: boolean
  onToggle?: (item: T) => void
  children: ReactNode
  className?: string
}

function SelectableCardWrapperInner<T extends { id: string }>({
  item,
  selected,
  onToggle,
  children,
  className,
}: SelectableCardWrapperProps<T>) {
  const { t } = useTranslation('records')

  return (
    <div
      className={cn(
        'group/selectable relative rounded-lg transition-all',
        selected && 'ring-2 ring-primary ring-offset-2 ring-offset-background',
        className
      )}
    >
      {/* 勾选复选框：外层只阻止冒泡到卡片点击，避免与 Checkbox.onCheckedChange 双重触发抵消 */}
      <div
        className={cn(
          'absolute start-2 top-2 z-30 flex size-6 items-center justify-center rounded-md bg-background/80 shadow-md backdrop-blur-xs transition-all',
          selected
            ? 'bg-background opacity-100 ring-2 ring-primary'
            : 'opacity-70 group-hover/selectable:opacity-100 hover:bg-background'
        )}
        onClick={(e) => {
          e.stopPropagation()
          if (e.target === e.currentTarget) {
            onToggle?.(item)
          }
        }}
      >
        <Checkbox
          checked={selected}
          onCheckedChange={() => onToggle?.(item)}
          aria-label={t('selection.selectCard', {
            id: item.id,
            defaultValue: `Select record ${item.id}`,
          })}
          className='size-4 bg-background/90 data-[state=checked]:bg-primary'
        />
      </div>

      {children}
    </div>
  )
}

export const SelectableCardWrapper = memo(
  SelectableCardWrapperInner
) as typeof SelectableCardWrapperInner
