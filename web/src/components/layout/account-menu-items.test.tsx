import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import i18n from '@/lib/i18n'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { AccountMenuItems } from './account-menu-items'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    // `asChild` 会把 role / tabIndex / onClick 透传给子节点，mock 必须继续转发。
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

async function renderItems(handlers: { onRequestSignOut: () => void }) {
  return render(
    <DropdownMenu open>
      <DropdownMenuTrigger>account</DropdownMenuTrigger>
      <DropdownMenuContent forceMount>
        <AccountMenuItems {...handlers} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

describe('AccountMenuItems', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    await i18n.changeLanguage('zh-Hans')
  })

  it.each([
    ['zh-Hans', '系统设置', '退出登录'],
    ['zh-Hant', '系統設定', '登出'],
    ['en', 'Settings', 'Sign out'],
  ])('translates the entries for %s', async (lang, settings, signOut) => {
    await i18n.changeLanguage(lang)
    const { getByText } = await renderItems({ onRequestSignOut: vi.fn() })

    await expect.element(getByText(settings)).toBeInTheDocument()
    await expect.element(getByText(signOut)).toBeInTheDocument()
  })

  it('links the settings entry to the appearance page', async () => {
    const { getByText } = await renderItems({ onRequestSignOut: vi.fn() })

    await expect
      .element(getByText(i18n.t('nav.Settings')))
      .toHaveAttribute('href', '/settings/appearance')
  })

  it('delegates the sign-out click to the parent shell', async () => {
    const onRequestSignOut = vi.fn()
    const { getByRole } = await renderItems({ onRequestSignOut })

    await userEvent.click(
      getByRole('menuitem', { name: i18n.t('account.signOut') })
    )

    expect(onRequestSignOut).toHaveBeenCalledOnce()
  })
})
