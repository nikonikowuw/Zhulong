import { type SVGProps } from 'react'
import { cn } from '@/lib/utils'

export interface ZhulongLogoProps extends SVGProps<SVGSVGElement> {
  useGradient?: boolean
}

export function ZhulongLogo({
  className,
  useGradient = false,
  ...props
}: ZhulongLogoProps) {
  const gradientId = 'zhulong-logo-z-grad'

  return (
    <svg
      viewBox='0 0 64 64'
      fill='none'
      xmlns='http://www.w3.org/2000/svg'
      className={cn('size-8 shrink-0', className)}
      {...props}
    >
      <title>Zhulong · 烛龙</title>
      {useGradient && (
        <defs>
          <linearGradient
            id={gradientId}
            x1='10'
            y1='8'
            x2='54'
            y2='56'
            gradientUnits='userSpaceOnUse'
          >
            <stop offset='0%' stopColor='#ff4757' />
            <stop offset='42%' stopColor='#ff793f' />
            <stop offset='100%' stopColor='#ff3366' />
          </linearGradient>
        </defs>
      )}
      <path
        d='M 14 8 H 48 C 51.3 8 54 10.7 54 14 V 18 C 54 19.8 53.2 21.5 51.8 22.6 L 27.2 38 H 48 C 51.3 38 54 40.7 54 44 V 50 C 54 53.3 51.3 56 48 56 H 16 C 12.7 56 10 53.3 10 50 V 46 C 10 44.2 10.8 42.5 12.2 41.4 L 36.8 26 H 16 C 12.7 26 10 23.3 10 20 V 14 C 10 10.7 12.7 8 16 8 Z'
        fill={useGradient ? `url(#${gradientId})` : 'currentColor'}
      />
    </svg>
  )
}
