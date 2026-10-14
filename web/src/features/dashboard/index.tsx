import { Link } from '@tanstack/react-router'
import {
  ArrowRight,
  Clock,
  HardDrive,
  Monitor,
  Network,
  Palette,
  ScrollText,
  Server,
  ShieldCheck,
  UserCheck,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'

export function Dashboard() {
  const { t } = useTranslation()

  const quickModules = [
    {
      title: t('nav.Cameras', { defaultValue: 'Cameras' }),
      description: t('dashboard.camerasDesc', {
        defaultValue: 'RTSP 摄像机流媒体接入、探活与通道管理',
      }),
      href: '/cameras',
      icon: Video,
      badge: '视频接入',
    },
    {
      title: t('nav.LiveView', { defaultValue: 'Live View' }),
      description: t('dashboard.liveDesc', {
        defaultValue: '多路低延迟实时视频网格与 WebCodecs 流媒体监看',
      }),
      href: '/live',
      icon: Monitor,
      badge: '实时画面',
    },
    {
      title: t('nav.Persons', { defaultValue: 'Persons' }),
      description: t('dashboard.personsDesc', {
        defaultValue: '人员底库管理、人脸图像导入与布控特征库维护',
      }),
      href: '/persons',
      icon: UserCheck,
      badge: '特征底库',
    },
    {
      title: t('nav.AuditLogs', { defaultValue: 'Audit Logs' }),
      description: t('dashboard.auditDesc', {
        defaultValue: '系统级安全行为与关键运维操作全生命周期审计',
      }),
      href: '/audit',
      icon: ScrollText,
      badge: '运维安全',
    },
  ]

  const systemSettings = [
    {
      title: t('nav.Network', { defaultValue: 'Network' }),
      desc: t('dashboard.networkDesc', { defaultValue: '网卡配置与看门狗' }),
      href: '/settings/network',
      icon: Network,
    },
    {
      title: t('nav.Storage', { defaultValue: 'Storage' }),
      desc: t('dashboard.storageDesc', { defaultValue: '分区容量与自动清理' }),
      href: '/settings/storage',
      icon: HardDrive,
    },
    {
      title: t('nav.Time', { defaultValue: 'Time & Clock' }),
      desc: t('dashboard.timeDesc', { defaultValue: 'NTP 校时与时区维护' }),
      href: '/settings/time',
      icon: Clock,
    },
    {
      title: t('nav.Appearance', { defaultValue: 'Appearance' }),
      desc: t('dashboard.appearanceDesc', {
        defaultValue: '深浅主题与视觉风格',
      }),
      href: '/settings/appearance',
      icon: Palette,
    },
  ]

  return (
    <>
      {/* ===== Top Heading ===== */}
      <Header>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      {/* ===== Main ===== */}
      <Main>
        <div className='mb-6 space-y-1'>
          <div className='flex items-center gap-2'>
            <h1 className='text-2xl font-bold tracking-tight md:text-3xl'>
              {t('nav.Dashboard', { defaultValue: 'Dashboard' })}
            </h1>
            <Badge
              variant='outline'
              className='gap-1 border-primary/30 text-primary'
            >
              <ShieldCheck className='size-3.5' />
              <span>Zhulong Edge</span>
            </Badge>
          </div>
          <p className='text-sm text-muted-foreground'>
            {t('dashboard.subtitle', {
              defaultValue:
                '边缘智能运维管理控制台 — 本地优先、实时流媒体与算力加速宿主',
            })}
          </p>
        </div>

        {/* 核心业务快速导航卡片 */}
        <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
          {quickModules.map((item) => (
            <Card
              key={item.href}
              className='group relative flex flex-col justify-between overflow-hidden border-border/70 transition-all hover:border-primary/50 hover:shadow-md'
            >
              <CardHeader className='pb-3'>
                <div className='flex items-center justify-between'>
                  <div className='flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary'>
                    <item.icon className='size-5' />
                  </div>
                  <Badge variant='secondary' className='text-xs font-normal'>
                    {item.badge}
                  </Badge>
                </div>
                <CardTitle className='pt-2 text-lg font-semibold group-hover:text-primary'>
                  {item.title}
                </CardTitle>
                <CardDescription className='line-clamp-2 text-xs'>
                  {item.description}
                </CardDescription>
              </CardHeader>
              <CardContent className='pt-0'>
                <Button
                  variant='ghost'
                  size='sm'
                  className='w-full justify-between px-2 text-xs'
                  asChild
                >
                  <Link to={item.href}>
                    <span>立即进入</span>
                    <ArrowRight className='size-3.5 transition-transform group-hover:translate-x-1' />
                  </Link>
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>

        {/* 系统概况与快速设置 */}
        <div className='mt-6 grid gap-6 md:grid-cols-2 lg:grid-cols-3'>
          <Card className='md:col-span-2'>
            <CardHeader>
              <div className='flex items-center gap-2'>
                <Server className='size-5 text-primary' />
                <CardTitle className='text-base font-semibold'>
                  系统快速配置
                </CardTitle>
              </div>
              <CardDescription>
                管理边缘网关的基础网络、存储配额、系统授时与界面外观
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className='grid gap-3 sm:grid-cols-2'>
                {systemSettings.map((setting) => (
                  <Link
                    key={setting.href}
                    to={setting.href}
                    className='flex items-center gap-3 rounded-lg border border-border/60 p-3 transition-colors hover:border-border hover:bg-muted/50'
                  >
                    <div className='flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-foreground'>
                      <setting.icon className='size-4' />
                    </div>
                    <div className='min-w-0 flex-1'>
                      <div className='text-sm leading-none font-medium'>
                        {setting.title}
                      </div>
                      <div className='mt-1 truncate text-xs text-muted-foreground'>
                        {setting.desc}
                      </div>
                    </div>
                    <ArrowRight className='size-4 text-muted-foreground' />
                  </Link>
                ))}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className='text-base font-semibold'>
                宿主运行特性
              </CardTitle>
              <CardDescription>Zhulong 边缘架构规格</CardDescription>
            </CardHeader>
            <CardContent className='space-y-3 text-xs'>
              <div className='flex items-center justify-between border-b pb-2'>
                <span className='text-muted-foreground'>架构设计</span>
                <span className='font-medium'>本地优先 (Local-First)</span>
              </div>
              <div className='flex items-center justify-between border-b pb-2'>
                <span className='text-muted-foreground'>运行时环境</span>
                <span className='font-medium'>Go + CGO 原生引擎</span>
              </div>
              <div className='flex items-center justify-between border-b pb-2'>
                <span className='text-muted-foreground'>媒体处理</span>
                <span className='font-medium'>硬件加速 + WebCodecs</span>
              </div>
              <div className='flex items-center justify-between'>
                <span className='text-muted-foreground'>运维模式</span>
                <span className='font-medium'>单机嵌入式运维</span>
              </div>
            </CardContent>
          </Card>
        </div>
      </Main>
    </>
  )
}
