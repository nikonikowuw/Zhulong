import { type ReactElement } from 'react'
import { Link } from '@tanstack/react-router'
import { LogOut, Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'

interface AccountMenuItemsProps {
  /** Asks the parent to open its own sign-out confirmation dialog. */
  onRequestSignOut: () => void
}

/**
 * 「设置 / 退出登录」两条目由顶部头像下拉与侧边栏用户菜单共用。
 * 两处外壳各挂一个 `<SignOutDialog>`，弹出层因此仍由父组件持有。
 */
export function AccountMenuItems({
  onRequestSignOut,
}: AccountMenuItemsProps): ReactElement {
  const { t } = useTranslation()

  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuGroup>
        <DropdownMenuItem asChild>
          <Link to='/settings/appearance'>
            <Settings className='me-2 size-4' />
            {t('nav.Settings')}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuGroup>
      <DropdownMenuSeparator />
      <DropdownMenuItem variant='destructive' onClick={onRequestSignOut}>
        <LogOut className='me-2 size-4' />
        {t('account.signOut')}
      </DropdownMenuItem>
    </>
  )
}
