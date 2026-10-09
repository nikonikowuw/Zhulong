import { Network, Settings } from 'lucide-react'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import i18n from '@/lib/i18n'
import { SidebarProvider } from '@/components/ui/sidebar'
import { NavGroup } from './nav-group'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useLocation: (opts?: { select?: (loc: { href: string }) => string }) =>
      opts?.select
        ? opts.select({ href: '/settings/network' })
        : { href: '/settings/network' },
    Link: ({
      to,
      children,
      ...props
    }: {
      to: string
      children: React.ReactNode
      [key: string]: unknown
    }) => (
      <a href={to} {...props}>
        {children}
      </a>
    ),
  }
})

describe('NavGroup i18n', () => {
  it('translates group and menu item titles based on active language', async () => {
    const items = [
      {
        title: 'Settings',
        icon: Settings,
        items: [
          {
            title: 'Network',
            url: '/settings/network',
            icon: Network,
          },
        ],
      },
    ]

    // 1. zh-Hans
    await i18n.changeLanguage('zh-Hans')
    const { getByText } = await render(
      <SidebarProvider>
        <NavGroup title='Other' items={items} />
      </SidebarProvider>
    )

    await expect.element(getByText('其他')).toBeInTheDocument()
    await expect.element(getByText('系统设置')).toBeInTheDocument()
    await expect.element(getByText('网络配置')).toBeInTheDocument()

    // 2. zh-Hant
    await i18n.changeLanguage('zh-Hant')
    await expect.element(getByText('其他')).toBeInTheDocument()
    await expect.element(getByText('系統設定')).toBeInTheDocument()
    await expect.element(getByText('網路配置')).toBeInTheDocument()

    // 3. en
    await i18n.changeLanguage('en')
    await expect.element(getByText('Other')).toBeInTheDocument()
    await expect.element(getByText('Settings')).toBeInTheDocument()
    await expect.element(getByText('Network')).toBeInTheDocument()
  })
})
