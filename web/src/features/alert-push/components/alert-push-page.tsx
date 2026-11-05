import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  BellRing,
  Plus,
  RefreshCw,
  Loader2,
  AlertCircle,
  Play,
  CheckCircle2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { alertPushApi } from '../api/alert-push-api'
import type { AlertDestination, TestDestinationResult } from '../types'
import { AlertDestinationDialog } from './alert-destination-dialog'
import { AlertDestinationsTable } from './alert-destinations-table'

const MAX_DESTINATIONS = 32

export function AlertPushPage() {
  const { t } = useTranslation('alertPush')
  const queryClient = useQueryClient()

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

  const handleCreate = () => {
    setEditingDest(null)
    setDialogOpen(true)
  }

  const handleEdit = (dest: AlertDestination) => {
    setEditingDest(dest)
    setDialogOpen(true)
  }

  const handleToggle = (dest: AlertDestination, enabled: boolean) => {
    if (enabled) {
      enableMutation.mutate(dest.id)
    } else {
      disableMutation.mutate(dest.id)
    }
  }

  const handleQuickTest = async (dest: AlertDestination) => {
    setTestModalDest(dest)
    setTestingQuick(true)
    setQuickTestResult(null)

    try {
      const res = await alertPushApi.test({ id: dest.id })
      setQuickTestResult(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setQuickTestResult({ ok: false, latencyMs: 0, reason: msg })
    } finally {
      setTestingQuick(false)
      // Refresh list to show updated status
      void queryClient.invalidateQueries({ queryKey: ['alert-destinations'] })
    }
  }

  const isLimitReached = destinations.length >= MAX_DESTINATIONS

  return (
    <div className='space-y-6'>
      {/* Top Header */}
      <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
        <div>
          <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
          <p className='text-sm text-muted-foreground'>{t('description')}</p>
        </div>

        <div className='flex items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => refetch()}
            disabled={isLoading || isRefetching}
            className='gap-1.5'
          >
            <RefreshCw
              className={`size-4 ${isRefetching ? 'animate-spin' : ''}`}
            />
            {t('refresh')}
          </Button>

          <Button
            size='sm'
            onClick={handleCreate}
            disabled={isLimitReached}
            className='gap-1.5'
          >
            <Plus className='size-4' />
            {t('create')}
          </Button>
        </div>
      </div>

      {isLimitReached && (
        <div className='rounded-md border border-amber-500/20 bg-amber-500/10 p-3 text-sm text-amber-600 dark:text-amber-400'>
          {t('maxLimitReached')}
        </div>
      )}

      {error && (
        <div className='flex items-center gap-2 rounded-md bg-destructive/15 p-4 text-sm text-destructive'>
          <AlertCircle className='size-5 shrink-0' />
          <span>{error instanceof Error ? error.message : String(error)}</span>
        </div>
      )}

      {/* Main Content */}
      {isLoading ? (
        <div className='flex h-64 items-center justify-center'>
          <Loader2 className='size-8 animate-spin text-muted-foreground' />
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
          onDelete={(dest) => setDeletingDest(dest)}
          onToggle={handleToggle}
          onTest={handleQuickTest}
        />
      )}

      {/* Create / Edit Dialog */}
      <AlertDestinationDialog
        open={dialogOpen}
        destination={editingDest}
        onOpenChange={setDialogOpen}
        onSuccess={() => {
          queryClient.invalidateQueries({ queryKey: ['alert-destinations'] })
        }}
      />

      {/* Delete Confirmation Dialog */}
      <Dialog
        open={Boolean(deletingDest)}
        onOpenChange={(open) => !open && setDeletingDest(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('deleteConfirmTitle')}</DialogTitle>
            <DialogDescription>
              {t('deleteConfirmDesc')}
              {deletingDest && (
                <span className='mt-2 block font-medium text-foreground'>
                  {deletingDest.name}
                </span>
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant='outline'
              onClick={() => setDeletingDest(null)}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              variant='destructive'
              onClick={() =>
                deletingDest && deleteMutation.mutate(deletingDest.id)
              }
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending && (
                <Loader2 className='mr-1.5 size-4 animate-spin' />
              )}
              {t('delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Quick Test Feedback Dialog */}
      <Dialog
        open={Boolean(testModalDest)}
        onOpenChange={(open) => !open && setTestModalDest(null)}
      >
        <DialogContent className='sm:max-w-md'>
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
                className={`flex items-center gap-3 rounded-lg p-4 text-sm ${
                  quickTestResult.ok
                    ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
                    : 'bg-destructive/15 text-destructive'
                }`}
              >
                {quickTestResult.ok ? (
                  <CheckCircle2 className='size-6 shrink-0' />
                ) : (
                  <AlertCircle className='size-6 shrink-0' />
                )}
                <div>
                  <div className='font-semibold'>
                    {quickTestResult.ok
                      ? t('testSuccess', { latency: quickTestResult.latencyMs })
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
            <Button onClick={() => setTestModalDest(null)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
