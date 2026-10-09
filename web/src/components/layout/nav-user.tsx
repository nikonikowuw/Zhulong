import { type ReactElement } from 'react'
import { Link } from '@tanstack/react-router'
import {
  BadgeCheck,
  Bell,
  ChevronsUpDown,
  CreditCard,
  LogOut,
  Sparkles,
} from 'lucide-react'
import { useAuthStore } from '@/stores/auth-store'
import useDialogState from '@/hooks/use-dialog-state'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { SignOutDialog } from '@/components/sign-out-dialog'

interface NavUserProps {
  user: {
    name: string
    email: string
    avatar: string
  }
}

interface UserIdentityBlockProps {
  avatar: string
  name: string
  role: string
  fallback: string
}

function UserIdentityBlock({
  avatar,
  name,
  role,
  fallback,
}: UserIdentityBlockProps): ReactElement {
  return (
    <>
      <Avatar className='h-8 w-8 rounded-lg'>
        <AvatarImage src={avatar} alt={name} />
        <AvatarFallback className='rounded-lg'>{fallback}</AvatarFallback>
      </Avatar>
      <div className='grid flex-1 text-start text-sm leading-tight'>
        <span className='truncate font-semibold'>{name}</span>
        <span className='truncate text-xs text-muted-foreground'>{role}</span>
      </div>
    </>
  )
}

export function NavUser({ user }: NavUserProps): ReactElement {
  const { isMobile } = useSidebar()
  const [open, setOpen] = useDialogState()
  const { auth } = useAuthStore()

  const displayName = auth.user?.username || user.name
  const displayRole = auth.user ? '系统管理员' : user.email
  const fallbackLetters = displayName.slice(0, 2).toUpperCase()

  return (
    <>
      <SidebarMenu>
        <SidebarMenuItem>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <SidebarMenuButton
                size='lg'
                className='data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground'
              >
                <UserIdentityBlock
                  avatar={user.avatar}
                  name={displayName}
                  role={displayRole}
                  fallback={fallbackLetters}
                />
                <ChevronsUpDown className='ms-auto size-4' />
              </SidebarMenuButton>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              className='w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg'
              side={isMobile ? 'bottom' : 'right'}
              align='end'
              sideOffset={4}
            >
              <DropdownMenuLabel className='p-0 font-normal'>
                <div className='flex items-center gap-2 px-1 py-1.5 text-start text-sm'>
                  <UserIdentityBlock
                    avatar={user.avatar}
                    name={displayName}
                    role={displayRole}
                    fallback={fallbackLetters}
                  />
                </div>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem>
                  <Sparkles />
                  Upgrade to Pro
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem asChild>
                  <Link to='/settings/account'>
                    <BadgeCheck />
                    Account
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <Link to='/settings'>
                    <CreditCard />
                    Billing
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <Link to='/settings/notifications'>
                    <Bell />
                    Notifications
                  </Link>
                </DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant='destructive'
                onClick={() => setOpen(true)}
              >
                <LogOut />
                Sign out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </SidebarMenuItem>
      </SidebarMenu>

      <SignOutDialog open={!!open} onOpenChange={setOpen} />
    </>
  )
}
