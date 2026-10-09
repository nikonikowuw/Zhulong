import { useMemo } from 'react'
import { Outlet } from '@tanstack/react-router'
import { Monitor, Bell, Palette, Wrench, UserCog, Network } from 'lucide-react'
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
        title: t('nav.Profile', { defaultValue: 'Profile' }),
        href: '/settings',
        icon: <UserCog size={18} />,
      },
      {
        title: t('nav.Account', { defaultValue: 'Account' }),
        href: '/settings/account',
        icon: <Wrench size={18} />,
      },
      {
        title: t('nav.Appearance', { defaultValue: 'Appearance' }),
        href: '/settings/appearance',
        icon: <Palette size={18} />,
      },
      {
        title: t('nav.Notifications', { defaultValue: 'Notifications' }),
        href: '/settings/notifications',
        icon: <Bell size={18} />,
      },
      {
        title: t('nav.Display', { defaultValue: 'Display' }),
        href: '/settings/display',
        icon: <Monitor size={18} />,
      },
      {
        title: t('nav.Network', { defaultValue: 'Network' }),
        href: '/settings/network',
        icon: <Network size={18} />,
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
                'Manage your account settings and set e-mail preferences.',
            })}
          </p>
        </div>
        <Separator className='my-4 lg:my-6' />
        <div className='flex flex-1 flex-col space-y-2 overflow-hidden md:space-y-2 lg:flex-row lg:space-y-0 lg:space-x-12'>
          <aside className='top-0 lg:sticky lg:w-1/5'>
            <SidebarNav items={sidebarNavItems} />
          </aside>
          <div className='flex w-full overflow-y-hidden p-1'>
            <Outlet />
          </div>
        </div>
      </Main>
    </>
  )
}
