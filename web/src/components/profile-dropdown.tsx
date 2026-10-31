import { useAccountIdentity } from '@/hooks/use-account-identity'
import useDialogState from '@/hooks/use-dialog-state'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { AccountMenuItems } from '@/components/layout/account-menu-items'
import { SignOutDialog } from '@/components/sign-out-dialog'

export function ProfileDropdown() {
  const [open, setOpen] = useDialogState()
  const { displayName, displayRole, initials } = useAccountIdentity()

  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            variant='ghost'
            className='relative h-8 w-8 rounded-full'
            aria-label={displayName}
            title={displayName}
          >
            <Avatar className='h-8 w-8'>
              <AvatarFallback className='bg-primary/10 text-xs font-semibold text-primary'>
                {initials}
              </AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent className='w-56' align='end' forceMount>
          <DropdownMenuLabel className='font-normal'>
            <div className='flex flex-col gap-1.5'>
              <p className='text-sm leading-none font-medium'>{displayName}</p>
              <p className='text-xs leading-none text-muted-foreground'>
                {displayRole}
              </p>
            </div>
          </DropdownMenuLabel>
          <AccountMenuItems onRequestSignOut={() => setOpen(true)} />
        </DropdownMenuContent>
      </DropdownMenu>

      <SignOutDialog open={!!open} onOpenChange={setOpen} />
    </>
  )
}
