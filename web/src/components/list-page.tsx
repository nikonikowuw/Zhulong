import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'

type ListPageProps = {
  title: string
  description?: React.ReactNode
  /** 标题行右侧主操作，通常是 `<XxxPrimaryButtons />`。 */
  actions?: React.ReactNode
  children: React.ReactNode
}

/**
 * CRUD 列表页的统一外壳。
 *
 * 把「顶栏 + 标题行」从每个 `features/<feature>/index.tsx` 里抽出来，是因为这段结构在
 * 页面间只靠评审肉眼保持一致：`min-w-0 flex-1` 一漏，长描述就会把主操作按钮
 * 挤到第二行最左。这里固定结构、只暴露内容插槽，规则才有落点。
 *
 * 需要响应式重排的表格工具栏等内容请放进 `children`，不要改这个外壳的类名。
 */
export function ListPage({
  title,
  description,
  actions,
  children,
}: ListPageProps): React.JSX.Element {
  return (
    <>
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      <Main className='flex flex-1 flex-col gap-4 sm:gap-6'>
        <div
          data-slot='list-page-header'
          className='flex flex-wrap items-end justify-between gap-2'
        >
          <div data-slot='list-page-heading' className='min-w-0 flex-1'>
            <h2 className='text-2xl font-bold tracking-tight'>{title}</h2>
            {description ? (
              <p className='max-w-3xl text-sm text-muted-foreground'>
                {description}
              </p>
            ) : null}
          </div>
          {actions ? (
            <div
              data-slot='list-page-actions'
              className='flex items-center gap-2'
            >
              {actions}
            </div>
          ) : null}
        </div>

        {children}
      </Main>
    </>
  )
}
