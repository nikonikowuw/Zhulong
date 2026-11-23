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
      {/* 衔尾昼夜环：时空回环与 24/7 守护 */}
      <path
        d='M 27 10 C 41 8 54 18 54 32 C 54 44.5 44 54 32 54 C 19.5 54 10 44 10 32 C 10 24 14.5 17 21 13'
        stroke='currentColor'
        strokeWidth={4.5}
        strokeLinecap='round'
      />
      {/* 烛龙回首龙角 / 破阴之火 */}
      <path
        d='M 27 10 C 23 10 19 6.5 17 4 C 18 8.5 16 12 13 14 C 17.5 14 21.5 12.5 24 10.5 Z'
        className='fill-brand-signal'
      />
      {/* 龙尾回卷珠 */}
      <circle cx={21} cy={13} r={2.25} fill='currentColor' />
      {/* 日蚀深渊光核底盘 */}
      <circle
        cx={32}
        cy={32}
        r={8.5}
        className='fill-background stroke-current/25'
        strokeWidth={1.5}
      />
      {/* 烛龙之目 / 激光赤核 */}
      <circle cx={32} cy={32} r={5} className='fill-brand-signal' />
      {/* 光学瞳孔高光点 */}
      <circle cx={33.8} cy={30.2} r={1.3} fill='#FFFFFF' />
    </svg>
  )
}
