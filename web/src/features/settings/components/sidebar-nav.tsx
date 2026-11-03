import { useState, type JSX } from 'react'
import { useLocation, useNavigate, Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'
import { buttonVariants } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

type SidebarNavProps = React.HTMLAttributes<HTMLElement> & {
  items: {
    href: string
    title: string
    icon: JSX.Element
  }[]
}

export function SidebarNav({ className, items, ...props }: SidebarNavProps) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const [val, setVal] = useState(pathname ?? '/settings/appearance')

  const handleSelect = (e: string) => {
    setVal(e)
    navigate({ to: e })
  }

  return (
    <>
      <div className='p-1 md:hidden'>
        <Select value={val} onValueChange={handleSelect}>
          <SelectTrigger className='h-12 sm:w-48'>
            <SelectValue
              placeholder={items.find((i) => i.href === val)?.title || ''}
            />
          </SelectTrigger>
          <SelectContent>
            {items.map((item) => (
              <SelectItem key={item.href} value={item.href}>
                <div className='flex gap-x-4 px-2 py-1'>
                  <span className='scale-125'>{item.icon}</span>
                  <span className='text-md'>{item.title}</span>
                </div>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <ScrollArea
        orientation='horizontal'
        type='always'
        className='hidden w-full min-w-40 bg-background px-1 py-2 md:block'
      >
        <nav
          className={cn('flex gap-1 py-1 lg:flex-col', className)}
          {...props}
        >
          {items.map((item) => {
            const isActive = pathname === item.href
            return (
              <Link
                key={item.href}
                to={item.href}
                className={cn(
                  buttonVariants({ variant: 'ghost' }),
                  isActive
                    ? 'bg-muted font-medium text-foreground shadow-2xs'
                    : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
                  'justify-start gap-2.5 px-3 py-2 text-sm transition-colors'
                )}
              >
                <span
                  className={cn(
                    'shrink-0',
                    isActive ? 'text-primary' : 'text-muted-foreground'
                  )}
                >
                  {item.icon}
                </span>
                <span>{item.title}</span>
              </Link>
            )
          })}
        </nav>
      </ScrollArea>
    </>
  )
}
