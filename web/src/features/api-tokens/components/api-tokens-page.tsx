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
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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
import { ContentSection } from '@/components/layout/content-section'
import { apiTokensApi } from '../api/api-tokens-api'
import { type APIToken, type APITokenCreateResponse } from '../data/schema'

const MAX_ACTIVE_TOKENS = 10

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
  const atLimit = activeCount >= MAX_ACTIVE_TOKENS

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
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='w-full'
    >
      <div className='space-y-5'>
        {/* 工具栏：配额 + 过滤 + 动作 */}
        <div className='flex flex-col gap-3 rounded-xl border border-border/80 bg-card/75 p-3 shadow-xs backdrop-blur-md sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex flex-wrap items-center gap-3'>
            <Badge
              variant='outline'
              className={cn(
                'h-6 gap-1.5 px-2 text-xs font-normal',
                atLimit
                  ? STATUS_LEVEL_CLASS.destructive
                  : STATUS_LEVEL_CLASS.active
              )}
            >
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  atLimit
                    ? STATUS_DOT_CLASS.destructive
                    : STATUS_DOT_CLASS.active
                )}
              />
              {t('limitTip', { count: activeCount })}
            </Badge>

            <div className='flex items-center gap-2'>
              <Checkbox
                id='show-revoked'
                checked={includeRevoked}
                onCheckedChange={(checked) => setIncludeRevoked(!!checked)}
              />
              <Label
                htmlFor='show-revoked'
                className='cursor-pointer text-xs font-normal text-muted-foreground'
              >
                {t('showRevoked')}
              </Label>
            </div>
          </div>

          <div className='flex items-center gap-2 self-end sm:self-auto'>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void refetch()}
              disabled={isLoading}
              className='h-8 gap-1.5 rounded-lg border-border/80 shadow-2xs'
              aria-label={t('common:actions.refresh', {
                defaultValue: '刷新',
              })}
            >
              <RefreshCw
                className={cn('size-3.5', isLoading && 'animate-spin')}
              />
              <span>
                {t('common:actions.refresh', { defaultValue: '刷新' })}
              </span>
            </Button>

            <Button
              size='sm'
              onClick={() => setCreateDialogOpen(true)}
              disabled={atLimit}
              className='h-8 gap-1.5 rounded-lg shadow-2xs'
            >
              <Plus className='size-3.5' />
              {t('createButton')}
            </Button>
          </div>
        </div>

        {/* 安全提示 */}
        <Alert className='border-amber-500/30 bg-amber-500/5 text-amber-900 dark:text-amber-200'>
          <ShieldAlert className='size-4 text-amber-600 dark:text-amber-400' />
          <AlertDescription className='text-xs leading-relaxed'>
            {t('securityNotice')}
          </AlertDescription>
        </Alert>

        {/* 凭据列表 */}
        <div className='rounded-xl border border-border/80 bg-card/75 shadow-xs backdrop-blur-md'>
          <Table>
            <TableHeader>
              <TableRow className='border-border/60 bg-muted/40 hover:bg-muted/40'>
                <TableHead className='h-9 ps-4 text-xs font-medium text-muted-foreground'>
                  {t('table.name')}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('table.maskedKey')}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('table.status')}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('table.lastUsedAt')}
                </TableHead>
                <TableHead className='h-9 text-xs font-medium text-muted-foreground'>
                  {t('table.createdAt')}
                </TableHead>
                <TableHead className='h-9 pe-4 text-end text-xs font-medium text-muted-foreground'>
                  {t('table.actions')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='h-32 text-center text-xs text-muted-foreground'
                  >
                    <RefreshCw className='mx-auto mb-2 size-4 animate-spin opacity-60' />
                    {t('table.loading', { defaultValue: '正在加载凭据…' })}
                  </TableCell>
                </TableRow>
              ) : tokens.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='h-40 text-center text-muted-foreground'
                  >
                    <div className='flex flex-col items-center justify-center gap-2'>
                      <div className='flex size-10 items-center justify-center rounded-xl border border-border/60 bg-muted/40'>
                        <Key className='size-5 text-muted-foreground/70' />
                      </div>
                      <p className='text-sm font-medium text-foreground'>
                        {t('table.empty')}
                      </p>
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
                    <TableRow
                      key={token.id}
                      className='border-border/50 transition-colors hover:bg-muted/30'
                    >
                      <TableCell className='py-3 ps-4 font-medium text-foreground'>
                        {token.name}
                      </TableCell>
                      <TableCell className='py-3'>
                        <code className='rounded-md border border-border/60 bg-muted/60 px-1.5 py-0.5 font-mono text-xs text-muted-foreground'>
                          {token.maskedKey}
                        </code>
                      </TableCell>
                      <TableCell className='py-3'>
                        <Badge
                          variant='outline'
                          className={cn(
                            'h-5 gap-1.5 px-1.5 text-[11px] font-normal',
                            isRevoked
                              ? STATUS_LEVEL_CLASS.inactive
                              : STATUS_LEVEL_CLASS.active
                          )}
                        >
                          <span
                            className={cn(
                              'size-1.5 rounded-full',
                              isRevoked
                                ? STATUS_DOT_CLASS.inactive
                                : STATUS_DOT_CLASS.active
                            )}
                          />
                          {isRevoked
                            ? t('table.statusRevoked')
                            : t('table.statusActive')}
                        </Badge>
                      </TableCell>
                      <TableCell className='py-3 font-mono text-xs text-muted-foreground'>
                        {token.lastUsedAt ? (
                          formatDate(token.lastUsedAt)
                        ) : (
                          <span className='text-muted-foreground/70 italic'>
                            {t('table.neverUsed')}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className='py-3 font-mono text-xs text-muted-foreground'>
                        {formatDate(token.createdAt)}
                      </TableCell>
                      <TableCell className='py-3 pe-4 text-end'>
                        {isRevoked ? (
                          <span className='text-xs text-muted-foreground/60'>
                            -
                          </span>
                        ) : (
                          <Button
                            variant='ghost'
                            size='sm'
                            onClick={() => setRevokingToken(token)}
                            className='h-7 px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive'
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
              <AlertTriangle className='size-5' />
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
                  <Check className='size-4 text-emerald-500' />
                ) : (
                  <Copy className='size-4' />
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
              <ShieldAlert className='size-5' />
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
    </ContentSection>
  )
}
