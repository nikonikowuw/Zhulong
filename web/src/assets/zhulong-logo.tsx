import { type SVGProps } from 'react'
import { cn } from '@/lib/utils'

export function ZhulongLogo({ className, ...props }: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox='0 0 32 32'
      fill='none'
      xmlns='http://www.w3.org/2000/svg'
      className={cn('size-8', className)}
      {...props}
    >
      {/* 外圈六边形工业感盾牌 */}
      <path
        d='M16 2L28 8.5V17.5C28 23.5 22.8 28.8 16 30C9.2 28.8 4 23.5 4 17.5V8.5L16 2Z'
        className='stroke-primary'
        strokeWidth='2'
        strokeLinecap='round'
        strokeLinejoin='round'
      />
      {/* 内部智能之眼 / 相机光圈光芒 */}
      <circle
        cx='16'
        cy='15'
        r='5.5'
        className='stroke-primary'
        strokeWidth='2'
      />
      <circle cx='16' cy='15' r='2.5' className='fill-primary' />
      {/* 聚焦准星刻度 */}
      <path
        d='M16 6V8M16 22V24M7 15H9M23 15H25'
        className='stroke-primary'
        strokeWidth='1.5'
        strokeLinecap='round'
      />
    </svg>
  )
}
