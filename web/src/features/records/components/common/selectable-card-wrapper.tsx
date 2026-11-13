import { memo, type ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'

export interface SelectableCardWrapperProps<T> {
  item: T
  selected: boolean
  onToggle?: (item: T) => void
  children: ReactNode
  className?: string
}

function SelectableCardWrapperInner<T>({
  item,
  selected,
  onToggle,
  children,
  className,
}: SelectableCardWrapperProps<T>) {
  return (
    <div
      className={cn(
        'group/selectable relative rounded-lg transition-all',
        selected && 'ring-2 ring-primary ring-offset-2 ring-offset-background',
        className
      )}
    >
      {/* 勾选复选框：选中时常驻高亮，未选中时轻度半透明常驻、hover 完全显现 */}
      <div
        className={cn(
          'absolute start-2 top-2 z-30 flex size-6 items-center justify-center rounded-md bg-background/80 shadow-md backdrop-blur-xs transition-all',
          selected
            ? 'bg-background opacity-100 ring-2 ring-primary'
            : 'opacity-70 group-hover/selectable:opacity-100 hover:bg-background'
        )}
        onClick={(e) => {
          e.stopPropagation()
          e.preventDefault()
          onToggle?.(item)
        }}
      >
        <Checkbox
          checked={selected}
          onCheckedChange={() => onToggle?.(item)}
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
