import { type SVGProps } from 'react'
import { cn } from '@/lib/utils'

export type ZhulongLogoProps = SVGProps<SVGSVGElement>

export function ZhulongLogo({ className, ...props }: ZhulongLogoProps) {
  return (
    <svg
      viewBox='0 0 64 64'
      fill='none'
      xmlns='http://www.w3.org/2000/svg'
      className={cn('size-8 shrink-0', className)}
      {...props}
    >
      <title>Zhulong · 烛龙</title>
      <path
        d='M 14 14 H 50 L 14 50 H 50'
        stroke='currentColor'
        strokeWidth={8}
        strokeLinecap='square'
        strokeLinejoin='miter'
      />
      <path d='M 28 28 H 36 V 36 H 28 Z' className='fill-brand-signal' />
    </svg>
  )
}
