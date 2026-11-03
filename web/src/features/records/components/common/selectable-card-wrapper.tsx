import React from 'react'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'

export interface SelectableCardWrapperProps {
  selected: boolean
  onToggle: () => void
  children: React.ReactNode
  className?: string
}

export function SelectableCardWrapper({
  selected,
  onToggle,
  children,
  className,
}: SelectableCardWrapperProps) {
  return (
    <div
      className={cn(
        'group/selectable relative rounded-lg transition-all',
        selected && 'ring-2 ring-primary ring-offset-2 ring-offset-background',
        className
      )}
    >
      {/* 勾选复选框：选中时常驻高亮，未选中时 hover 浮现 */}
      <div
        className={cn(
          'absolute start-2 top-2 z-30 flex size-6 items-center justify-center rounded-md bg-background/90 shadow-md backdrop-blur-xs transition-opacity',
          selected
            ? 'opacity-100 ring-1 ring-primary'
            : 'opacity-0 group-hover/selectable:opacity-100'
        )}
        onClick={(e) => {
          e.stopPropagation()
          e.preventDefault()
          onToggle()
        }}
      >
        <Checkbox
          checked={selected}
          onCheckedChange={() => onToggle()}
          className='size-4 bg-background data-[state=checked]:bg-primary'
        />
      </div>

      {children}
    </div>
  )
}
