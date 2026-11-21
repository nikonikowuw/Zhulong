import { type ReactElement } from 'react'
import { ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_LEVEL_CLASS, statusDotClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { type AuthUser } from '../api/auth-api'
import { InitForm } from './init-form'

interface SetupCardProps {
  onSuccess?: (user: AuthUser) => void
}

/**
 * 首次部署卡片：与登录卡片保持同一外形，但把「这台设备还没有管理员」
 * 提到第一视觉层——顶部强调条 + 「首次部署」徽章 + 动作化标题。
 *
 * 单独成组件的理由：初始化是**强制**路径（没有管理员就进不了控制台），
 * 而旧版只靠一个绿色盾牌图标区分，在机房照片背景上很容易被误认成普通登录页。
 * 颜色一律取自 status-palette（见 .trellis/spec/frontend/theme-and-i18n.md §1.3.3）。
 */
export function SetupCard({ onSuccess }: SetupCardProps): ReactElement {
  const { t } = useTranslation('auth')

  return (
    <Card className='relative gap-0 overflow-hidden border-border/70 bg-card/90 pt-0 pb-6 shadow-2xl backdrop-blur-xl'>
      {/* 顶部强调条：整屏唯一一处高饱和色块，3px 就够把两张卡片区分开 */}
      <div
        aria-hidden='true'
        className={cn('h-1 w-full', statusDotClass('active'))}
      />

      {/* 一条细分隔线把「这是什么状态」和「你要填什么」分成两段，扫视时不会糊成一坨 */}
      <CardHeader className='gap-3 border-b border-border/60 px-6 pt-5 pb-4'>
        <div className='flex items-center gap-3'>
          <div
            className={cn(
              'flex size-10 shrink-0 items-center justify-center rounded-xl border',
              STATUS_LEVEL_CLASS.active
            )}
          >
            <ShieldCheck className='size-5' aria-hidden='true' />
          </div>
          <div className='min-w-0 space-y-1'>
            <CardTitle className='text-xl leading-tight'>
              {t('init.cardTitle')}
            </CardTitle>
            <CardDescription className='text-xs'>
              {t('init.cardDescription')}
            </CardDescription>
          </div>
        </div>

        <div className='flex flex-wrap items-center gap-x-2 gap-y-1'>
          <Badge
            variant='outline'
            className={cn('h-5 rounded-full px-2', STATUS_LEVEL_CLASS.active)}
          >
            {t('init.badge')}
          </Badge>
          <span className='text-xs text-muted-foreground'>
            {t('init.badgeHint')}
          </span>
        </div>
      </CardHeader>

      <CardContent className='px-6'>
        <InitForm onSuccess={onSuccess} />
      </CardContent>
    </Card>
  )
}
