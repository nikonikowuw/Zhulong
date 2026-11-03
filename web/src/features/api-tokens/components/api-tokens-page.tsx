import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  Check,
  Copy,
  Key,
  Plus,
  RefreshCw,
  ShieldAlert,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { apiTokensApi } from '../api/api-tokens-api'
import { type APIToken, type APITokenCreateResponse } from '../data/schema'

export function APITokensPage() {
  const { t, i18n } = useTranslation('apiTokens')
  const queryClient = useQueryClient()
  const [includeRevoked, setIncludeRevoked] = useState(false)
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [tokenName, setTokenName] = useState('')
  const [createdSecret, setCreatedSecret] =
    useState<APITokenCreateResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [revokingToken, setRevokingToken] = useState<APIToken | null>(null)

  const {
    data: tokens = [],
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ['api-tokens', includeRevoked],
    queryFn: () => apiTokensApi.listTokens(includeRevoked),
  })

  const activeCount = tokens.filter((t) => !t.revokedAt).length

  const createMutation = useMutation({
    mutationFn: (name: string) => apiTokensApi.createToken(name),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['api-tokens'] })
      setCreateDialogOpen(false)
      setTokenName('')
      setCreatedSecret(data)
      toast.success(t('messages.createSuccess'))
    },
    onError: (err: Error) => {
      toast.error(err.message || t('messages.loadFailed'))
    },
  })

  const revokeMutation = useMutation({
    mutationFn: (id: string) => apiTokensApi.revokeToken(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['api-tokens'] })
      setRevokingToken(null)
      toast.success(t('messages.revokeSuccess'))
    },
    onError: (err: Error) => {
      toast.error(err.message || t('messages.loadFailed'))
    },
  })

  const handleCopy = async () => {
    if (!createdSecret?.token) return
    try {
      await navigator.clipboard.writeText(createdSecret.token)
      setCopied(true)
      toast.success(t('dialogs.copied'))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy')
    }
  }

  const formatDate = (dateStr?: string | null) => {
    if (!dateStr) return '-'
    const d = new Date(dateStr)
    return isNaN(d.getTime())
      ? '-'
      : d.toLocaleString(i18n.language || 'zh-Hans')
  }

  return (
    <div className='space-y-6'>
      {/* Header section */}
      <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
        <div className='space-y-1'>
          <div className='flex items-center gap-3'>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <Badge
              variant={activeCount >= 10 ? 'destructive' : 'secondary'}
              className='text-xs'
            >
              {t('limitTip', { count: activeCount })}
            </Badge>
          </div>
          <p className='text-sm text-muted-foreground'>{t('description')}</p>
        </div>

        <div className='flex items-center gap-3'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void refetch()}
            disabled={isLoading}
            className='gap-1.5'
          >
            <RefreshCw
              className={`h-4 w-4 ${isLoading ? 'animate-spin' : ''}`}
            />
          </Button>

          <Button
            onClick={() => setCreateDialogOpen(true)}
            disabled={activeCount >= 10}
            className='gap-1.5'
          >
            <Plus className='h-4 w-4' />
            {t('createButton')}
          </Button>
        </div>
      </div>

      {/* Security Warning Notice */}
      <Alert className='border-amber-500/30 bg-amber-500/5 text-amber-900 dark:text-amber-200'>
        <ShieldAlert className='h-4 w-4 text-amber-600 dark:text-amber-400' />
        <AlertDescription className='text-xs'>
          {t('securityNotice')}
        </AlertDescription>
      </Alert>

      {/* Filter Toolbar */}
      <div className='flex items-center space-x-2'>
        <Checkbox
          id='show-revoked'
          checked={includeRevoked}
          onCheckedChange={(checked) => setIncludeRevoked(!!checked)}
        />
        <Label
          htmlFor='show-revoked'
          className='cursor-pointer text-sm font-normal text-muted-foreground'
        >
          {t('showRevoked')}
        </Label>
      </div>

      {/* Table Section */}
      <div className='rounded-md border border-border bg-card'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-[200px]'>{t('table.name')}</TableHead>
              <TableHead>{t('table.maskedKey')}</TableHead>
              <TableHead>{t('table.status')}</TableHead>
              <TableHead>{t('table.lastUsedAt')}</TableHead>
              <TableHead>{t('table.createdAt')}</TableHead>
              <TableHead className='text-right'>{t('table.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className='h-32 text-center text-muted-foreground'
                >
                  <div className='flex flex-col items-center justify-center gap-1.5'>
                    <Key className='h-6 w-6 text-muted-foreground/60' />
                    <p className='text-sm font-medium'>{t('table.empty')}</p>
                    <p className='text-xs text-muted-foreground'>
                      {t('table.emptyDesc')}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              tokens.map((token) => {
                const isRevoked = !!token.revokedAt
                return (
                  <TableRow key={token.id}>
                    <TableCell className='font-medium text-foreground'>
                      {token.name}
                    </TableCell>
                    <TableCell>
                      <code className='rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground'>
                        {token.maskedKey}
                      </code>
                    </TableCell>
                    <TableCell>
                      {isRevoked ? (
                        <Badge
                          variant='outline'
                          className='text-xs text-muted-foreground'
                        >
                          {t('table.statusRevoked')}
                        </Badge>
                      ) : (
                        <Badge
                          variant='outline'
                          className='border-green-500/30 bg-green-500/10 text-xs text-green-600 dark:text-green-400'
                        >
                          {t('table.statusActive')}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className='text-sm text-muted-foreground'>
                      {token.lastUsedAt
                        ? formatDate(token.lastUsedAt)
                        : t('table.neverUsed')}
                    </TableCell>
                    <TableCell className='text-sm text-muted-foreground'>
                      {formatDate(token.createdAt)}
                    </TableCell>
                    <TableCell className='text-right'>
                      {isRevoked ? (
                        <span className='text-xs text-muted-foreground'>-</span>
                      ) : (
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() => setRevokingToken(token)}
                          className='text-xs text-destructive hover:bg-destructive/10 hover:text-destructive'
                        >
                          {t('dialogs.confirmRevoke')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      {/* Create Dialog */}
      <Dialog open={createDialogOpen} onOpenChange={setCreateDialogOpen}>
        <DialogContent className='sm:max-w-[425px]'>
          <DialogHeader>
            <DialogTitle>{t('dialogs.createTitle')}</DialogTitle>
            <DialogDescription>{t('dialogs.createDesc')}</DialogDescription>
          </DialogHeader>
          <div className='space-y-4 py-2'>
            <div className='space-y-2'>
              <Label htmlFor='token-name'>{t('dialogs.nameLabel')}</Label>
              <Input
                id='token-name'
                value={tokenName}
                onChange={(e) => setTokenName(e.target.value)}
                placeholder={t('dialogs.namePlaceholder')}
                maxLength={48}
                autoFocus
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              variant='outline'
              onClick={() => setCreateDialogOpen(false)}
            >
              {t('dialogs.cancel')}
            </Button>
            <Button
              disabled={
                !tokenName.trim() ||
                tokenName.trim().length > 48 ||
                createMutation.isPending
              }
              onClick={() => createMutation.mutate(tokenName.trim())}
            >
              {t('dialogs.confirmCreate')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Secret Display Dialog (Shown Once) */}
      <Dialog
        open={!!createdSecret}
        onOpenChange={(open) => {
          if (!open) setCreatedSecret(null)
        }}
      >
        <DialogContent className='sm:max-w-[550px]'>
          <DialogHeader>
            <div className='flex items-center gap-2 text-amber-500'>
              <AlertTriangle className='h-5 w-5' />
              <DialogTitle className='text-foreground'>
                {t('dialogs.showSecretTitle')}
              </DialogTitle>
            </div>
            <DialogDescription className='font-medium text-amber-600 dark:text-amber-400'>
              {t('dialogs.showSecretDesc')}
            </DialogDescription>
          </DialogHeader>

          <div className='space-y-3 py-2'>
            <Label className='text-xs font-semibold'>
              {t('dialogs.tokenLabel')} ({createdSecret?.name})
            </Label>
            <div className='flex items-center gap-2'>
              <Input
                readOnly
                value={createdSecret?.token || ''}
                className='bg-muted font-mono text-xs select-all'
              />
              <Button
                type='button'
                variant='outline'
                size='icon'
                onClick={handleCopy}
                className='shrink-0'
              >
                {copied ? (
                  <Check className='h-4 w-4 text-green-500' />
                ) : (
                  <Copy className='h-4 w-4' />
                )}
              </Button>
            </div>
          </div>

          <DialogFooter>
            <Button onClick={() => setCreatedSecret(null)}>
              {t('dialogs.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Revoke Confirmation Dialog */}
      <Dialog
        open={!!revokingToken}
        onOpenChange={(open) => {
          if (!open) setRevokingToken(null)
        }}
      >
        <DialogContent className='sm:max-w-[425px]'>
          <DialogHeader>
            <div className='flex items-center gap-2 text-destructive'>
              <ShieldAlert className='h-5 w-5' />
              <DialogTitle>{t('dialogs.revokeTitle')}</DialogTitle>
            </div>
            <DialogDescription>
              {t('dialogs.revokeDesc', { name: revokingToken?.name })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant='outline'
              onClick={() => setRevokingToken(null)}
              disabled={revokeMutation.isPending}
            >
              {t('dialogs.cancel')}
            </Button>
            <Button
              variant='destructive'
              onClick={() =>
                revokingToken && revokeMutation.mutate(revokingToken.id)
              }
              disabled={revokeMutation.isPending}
            >
              {t('dialogs.confirmRevoke')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
