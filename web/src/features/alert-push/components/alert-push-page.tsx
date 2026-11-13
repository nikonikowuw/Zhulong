import { useCallback, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertCircle,
  BellRing,
  CheckCircle2,
  Loader2,
  Play,
  Plus,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ContentSection } from '@/components/layout/content-section'
import { alertPushApi } from '../api/alert-push-api'
import type { AlertDestination, TestDestinationResult } from '../types'
import { AlertDestinationDialog } from './alert-destination-dialog'
import { AlertDestinationsTable } from './alert-destinations-table'

const MAX_DESTINATIONS = 32

export function AlertPushPage() {
  const { t } = useTranslation('alertPush')
  const queryClient = useQueryClient()

  // Dialog State
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingDest, setEditingDest] = useState<AlertDestination | null>(null)
  const [deletingDest, setDeletingDest] = useState<AlertDestination | null>(
    null
  )

  // Quick Test Dialog
  const [testModalDest, setTestModalDest] = useState<AlertDestination | null>(
    null
  )
  const [testingQuick, setTestingQuick] = useState(false)
  const [quickTestResult, setQuickTestResult] =
    useState<TestDestinationResult | null>(null)
  const quickTestSeqRef = useRef(0)

  // Queries
  const {
    data: destinations = [],
    isLoading,
    isRefetching,
    refetch,
    error,
  } = useQuery({
    queryKey: ['alert-destinations'],
    queryFn: () => alertPushApi.list(),
    refetchInterval: 10000, // Poll status every 10s
  })

  // Mutations
  const enableMutation = useMutation({
    mutationFn: (id: string) => alertPushApi.enable(id),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['alert-destinations'] }),
  })

  const disableMutation = useMutation({
    mutationFn: (id: string) => alertPushApi.disable(id),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['alert-destinations'] }),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => alertPushApi.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alert-destinations'] })
      setDeletingDest(null)
    },
  })

  const { mutate: enableDest } = enableMutation
  const { mutate: disableDest } = disableMutation

  const handleCreate = useCallback(() => {
    setEditingDest(null)
    setDialogOpen(true)
  }, [])

  const handleEdit = useCallback((dest: AlertDestination) => {
    setEditingDest(dest)
    setDialogOpen(true)
  }, [])

  const handleDeletePrompt = useCallback((dest: AlertDestination) => {
    setDeletingDest(dest)
  }, [])

  const handleToggle = useCallback(
    (dest: AlertDestination, enabled: boolean) => {
      if (enabled) {
        enableDest(dest.id)
      } else {
        disableDest(dest.id)
      }
    },
    [enableDest, disableDest]
  )

  const handleQuickTest = useCallback(
    async (dest: AlertDestination) => {
      const seq = ++quickTestSeqRef.current
      setTestModalDest(dest)
      setTestingQuick(true)
      setQuickTestResult(null)

      try {
        const res = await alertPushApi.test({ id: dest.id })
        if (seq === quickTestSeqRef.current) {
          setQuickTestResult(res)
        }
      } catch (err: unknown) {
        if (seq === quickTestSeqRef.current) {
          const msg = err instanceof Error ? err.message : String(err)
          setQuickTestResult({ ok: false, latencyMs: 0, reason: msg })
        }
      } finally {
        if (seq === quickTestSeqRef.current) {
          setTestingQuick(false)
          void queryClient.invalidateQueries({
            queryKey: ['alert-destinations'],
          })
        }
      }
    },
    [queryClient]
  )

  const handleDialogSuccess = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ['alert-destinations'] })
  }, [queryClient])

  const isLimitReached = destinations.length >= MAX_DESTINATIONS
  const quotaLevel = isLimitReached ? 'destructive' : 'active'
  const errorMessage = error instanceof Error ? error.message : String(error)

  return (
    <ContentSection
      title={t('title')}
      desc={t('description')}
      className='w-full'
    >
      <div className='space-y-5'>
        {/* 工具栏：目标配额 + 刷新 + 新建 */}
        <div className='flex flex-col gap-3 rounded-xl border border-border/80 bg-card/75 p-3 shadow-xs backdrop-blur-md sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge
              variant='outline'
              className={cn(
                'h-6 gap-1.5 px-2 text-xs font-normal',
                statusLevelClass(quotaLevel)
              )}
            >
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  statusDotClass(quotaLevel)
                )}
              />
              {isLimitReached
                ? t('maxLimitReached')
                : t('limitUsage', {
                    count: destinations.length,
                    max: MAX_DESTINATIONS,
                  })}
            </Badge>
          </div>

          <div className='flex items-center gap-2 self-end sm:self-auto'>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void refetch()}
              disabled={isLoading || isRefetching}
              className='h-8 gap-1.5 rounded-lg border-border/80 shadow-2xs'
            >
              <RefreshCw
                className={`size-3.5 ${isRefetching ? 'animate-spin' : ''}`}
              />
              <span>{t('refresh')}</span>
            </Button>

            <Button
              size='sm'
              onClick={handleCreate}
              disabled={isLimitReached}
              className='h-8 gap-1.5 rounded-lg shadow-2xs'
            >
              <Plus className='size-3.5' />
              <span>{t('create')}</span>
            </Button>
          </div>
        </div>

        {/* 后台刷新失败且已有缓存数据时，顶部提示错误但不遮盖现有列表 */}
        {error && destinations.length > 0 ? (
          <div
            className={cn(
              'flex items-center justify-between gap-3 rounded-xl border p-4 text-sm',
              statusLevelClass('destructive')
            )}
          >
            <div className='flex items-center gap-2'>
              <AlertCircle className='size-5 shrink-0' />
              <span>{errorMessage}</span>
            </div>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void refetch()}
              disabled={isRefetching}
            >
              {t('actions.retry')}
            </Button>
          </div>
        ) : null}

        {/* Main Content：Loading / Error / Empty / Table 四态互斥 */}
        {isLoading ? (
          <div className='flex h-64 items-center justify-center'>
            <Loader2 className='size-8 animate-spin text-muted-foreground' />
          </div>
        ) : error && destinations.length === 0 ? (
          <div
            className={cn(
              'flex min-h-[300px] flex-col items-center justify-center gap-4 rounded-lg border p-8 text-center',
              statusLevelClass('destructive')
            )}
          >
            <AlertCircle className='size-8 shrink-0' />
            <p className='max-w-md text-sm'>{errorMessage}</p>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void refetch()}
              disabled={isRefetching}
              className='gap-1.5'
            >
              <RefreshCw
                className={`size-3.5 ${isRefetching ? 'animate-spin' : ''}`}
              />
              <span>{t('actions.retry')}</span>
            </Button>
          </div>
        ) : destinations.length === 0 ? (
          <div className='flex min-h-[360px] animate-in flex-col items-center justify-center rounded-lg border border-dashed p-8 text-center fade-in-50'>
            <div className='flex size-14 items-center justify-center rounded-full bg-muted'>
              <BellRing className='size-7 text-muted-foreground' />
            </div>
            <h3 className='mt-4 text-lg font-semibold'>{t('empty')}</h3>
            <p className='mt-2 mb-4 max-w-sm text-sm text-muted-foreground'>
              {t('emptyHint')}
            </p>
            <Button onClick={handleCreate} className='gap-2'>
              <Plus className='size-4' />
              {t('create')}
            </Button>
          </div>
        ) : (
          <AlertDestinationsTable
            destinations={destinations}
            onEdit={handleEdit}
            onDelete={handleDeletePrompt}
            onToggle={handleToggle}
            onTest={handleQuickTest}
          />
        )}

        {/* Create / Edit Dialog */}
        <AlertDestinationDialog
          open={dialogOpen}
          destination={editingDest}
          onOpenChange={setDialogOpen}
          onSuccess={handleDialogSuccess}
        />

        {/* Delete Confirmation Dialog */}
        <Dialog
          open={Boolean(deletingDest)}
          onOpenChange={(open) => (!open ? setDeletingDest(null) : undefined)}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t('deleteConfirmTitle')}</DialogTitle>
              <DialogDescription>
                {t('deleteConfirmDesc')}
                {deletingDest ? (
                  <span className='mt-2 block font-medium text-foreground'>
                    {deletingDest.name}
                  </span>
                ) : null}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button
                variant='outline'
                onClick={() => setDeletingDest(null)}
                disabled={deleteMutation.isPending}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                variant='destructive'
                onClick={() =>
                  deletingDest
                    ? deleteMutation.mutate(deletingDest.id)
                    : undefined
                }
                disabled={deleteMutation.isPending}
              >
                {deleteMutation.isPending ? (
                  <Loader2 className='me-1.5 size-4 animate-spin' />
                ) : null}
                {t('delete')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Quick Test Feedback Dialog */}
        <Dialog
          open={Boolean(testModalDest)}
          onOpenChange={(open) => {
            if (!open) {
              quickTestSeqRef.current += 1
              setTestingQuick(false)
              setQuickTestResult(null)
              setTestModalDest(null)
            }
          }}
        >
          <DialogContent className='sm:max-w-md' aria-describedby={undefined}>
            <DialogHeader>
              <DialogTitle className='flex items-center gap-2'>
                <Play className='size-4 text-primary' />
                <span>
                  {t('test')} - {testModalDest?.name}
                </span>
              </DialogTitle>
            </DialogHeader>

            <div className='py-4'>
              {testingQuick ? (
                <div className='flex flex-col items-center justify-center gap-2 py-6 text-sm text-muted-foreground'>
                  <Loader2 className='size-8 animate-spin text-primary' />
                  <span>{t('testing')}</span>
                </div>
              ) : quickTestResult ? (
                <div
                  className={cn(
                    'flex items-center gap-3 rounded-lg border p-4 text-sm',
                    statusLevelClass(
                      quickTestResult.ok ? 'active' : 'destructive'
                    )
                  )}
                >
                  {quickTestResult.ok ? (
                    <CheckCircle2 className='size-6 shrink-0' />
                  ) : (
                    <AlertCircle className='size-6 shrink-0' />
                  )}
                  <div>
                    <div className='font-semibold'>
                      {quickTestResult.ok
                        ? t('testSuccess', {
                            latency: quickTestResult.latencyMs,
                          })
                        : t('testFailed', {
                            reason: quickTestResult.reason
                              ? t(`failureReasons.${quickTestResult.reason}`, {
                                  defaultValue: quickTestResult.reason,
                                })
                              : '',
                          })}
                    </div>
                  </div>
                </div>
              ) : null}
            </div>

            <DialogFooter>
              <Button
                onClick={() => {
                  quickTestSeqRef.current += 1
                  setTestingQuick(false)
                  setQuickTestResult(null)
                  setTestModalDest(null)
                }}
              >
                {t('actions.close')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </ContentSection>
  )
}
