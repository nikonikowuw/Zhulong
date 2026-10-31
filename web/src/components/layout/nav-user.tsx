import { type ReactElement } from 'react'
import { ChevronsUpDown } from 'lucide-react'
import { useAccountIdentity } from '@/hooks/use-account-identity'
import useDialogState from '@/hooks/use-dialog-state'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { SignOutDialog } from '@/components/sign-out-dialog'
import { AccountMenuItems } from './account-menu-items'

interface UserIdentityBlockProps {
  name: string
  role: string
  initials: string
}

function UserIdentityBlock({
  name,
  role,
  initials,
}: UserIdentityBlockProps): ReactElement {
  return (
    <>
      <Avatar className='h-8 w-8 rounded-lg'>
        <AvatarFallback className='rounded-lg bg-primary/10 text-xs font-semibold text-primary'>
          {initials}
        </AvatarFallback>
      </Avatar>
      <div className='grid flex-1 text-start text-sm leading-tight'>
        <span className='truncate font-semibold'>{name}</span>
        <span className='truncate text-xs text-muted-foreground'>{role}</span>
      </div>
    </>
  )
}

export function NavUser(): ReactElement {
  const { isMobile } = useSidebar()
  const [open, setOpen] = useDialogState()
  const { displayName, displayRole, initials } = useAccountIdentity()

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
                  name={displayName}
                  role={displayRole}
                  initials={initials}
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
                    name={displayName}
                    role={displayRole}
                    initials={initials}
                  />
                </div>
              </DropdownMenuLabel>
              <AccountMenuItems onRequestSignOut={() => setOpen(true)} />
            </DropdownMenuContent>
          </DropdownMenu>
        </SidebarMenuItem>
      </SidebarMenu>

      <SignOutDialog open={!!open} onOpenChange={setOpen} />
    </>
  )
}
