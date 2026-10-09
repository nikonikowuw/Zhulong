import { Separator } from '@/components/ui/separator'

type ContentSectionProps = {
  title: string
  desc: string
  className?: string
  children: React.ReactNode
}

export function ContentSection({
  title,
  desc,
  className = 'lg:max-w-xl',
  children,
}: ContentSectionProps) {
  return (
    <div className='flex flex-1 flex-col'>
      <div className='flex-none'>
        <h3 className='text-lg font-medium'>{title}</h3>
        <p className='text-sm text-muted-foreground'>{desc}</p>
      </div>
      <Separator className='my-4 flex-none' />
      <div className='faded-bottom h-full w-full overflow-y-auto scroll-smooth pe-4 pb-12'>
        <div className={`-mx-1 px-1.5 ${className}`}>{children}</div>
      </div>
    </div>
  )
}
