import { useMemo } from 'react'
import { Outlet } from '@tanstack/react-router'
import {
  Palette,
  Network,
  Clock,
  HardDrive,
  RadioTower,
  Activity,
  Key,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Separator } from '@/components/ui/separator'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { SidebarNav } from './components/sidebar-nav'

export function Settings() {
  const { t } = useTranslation()

  const sidebarNavItems = useMemo(
    () => [
      {
        title: t('nav.Appearance', { defaultValue: 'Appearance' }),
        href: '/settings/appearance',
        icon: <Palette size={18} />,
      },
      {
        title: t('nav.Network', { defaultValue: 'Network' }),
        href: '/settings/network',
        icon: <Network size={18} />,
      },
      {
        title: t('nav.Time', { defaultValue: 'Time & Clock' }),
        href: '/settings/time',
        icon: <Clock size={18} />,
      },
      {
        title: t('nav.Storage', { defaultValue: 'Storage & Lifecycle' }),
        href: '/settings/storage',
        icon: <HardDrive size={18} />,
      },
      {
        title: t('nav.Hardware', { defaultValue: 'System & Hardware' }),
        href: '/settings/system',
        icon: <Activity size={18} />,
      },
      {
        title: t('nav.GB28181', { defaultValue: 'GB28181 Ingress' }),
        href: '/settings/gb28181',
        icon: <RadioTower size={18} />,
      },
      {
        title: t('nav.APITokens', { defaultValue: 'API Tokens' }),
        href: '/settings/api-tokens',
        icon: <Key size={18} />,
      },
    ],
    [t]
  )

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

      <Main fixed>
        <div className='space-y-0.5'>
          <h1 className='text-2xl font-bold tracking-tight md:text-3xl'>
            {t('nav.settingsTitle', { defaultValue: 'Settings' })}
          </h1>
          <p className='text-muted-foreground'>
            {t('nav.settingsDesc', {
              defaultValue:
                'Configure system appearance, network interfaces, clock, and storage lifecycle.',
            })}
          </p>
        </div>
        <Separator className='my-4 lg:my-6' />
        <div className='flex flex-1 flex-col space-y-4 overflow-hidden lg:flex-row lg:gap-8 lg:space-y-0 xl:gap-10'>
          <aside className='top-0 shrink-0 lg:sticky lg:w-56 xl:w-64'>
            <SidebarNav items={sidebarNavItems} />
          </aside>
          <div className='flex min-w-0 flex-1 overflow-y-hidden p-1'>
            <Outlet />
          </div>
        </div>
      </Main>
    </>
  )
}
