import { useState } from 'react'
import { AlertCircle, Camera, Layers, Loader2, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { statusDotClass, statusLevelClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type Gb28181Device } from '../data/schema'
import {
  useAddGb28181ChannelCamera,
  useGb28181Channels,
  useSyncGb28181Catalog,
} from '../hooks/use-gb28181'
import { describeGb28181Error } from '../utils/error-message'

interface Gb28181ChannelsDrawerProps {
  device: Gb28181Device | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function Gb28181ChannelsDrawer({
  device,
  open,
  onOpenChange,
}: Gb28181ChannelsDrawerProps) {
  const { t } = useTranslation('gb28181')
  const [addingChannelId, setAddingChannelId] = useState<string | null>(null)

  const channelsQuery = useGb28181Channels(
    open && device ? device.deviceId : null
  )
  const syncMutation = useSyncGb28181Catalog()
  const addCameraMutation = useAddGb28181ChannelCamera()

  const isOffline = device?.state !== 'online'
  const channels = channelsQuery.data ?? []

  const handleSync = async () => {
    if (!device) return
    try {
      const res = await syncMutation.mutateAsync(device.deviceId)
      toast.success(t('channels.syncSuccess', { count: res.total }))
    } catch (err) {
      toast.error(describeGb28181Error(err, t) || t('channels.syncFailed'))
    }
  }

  const handleAddAsCamera = async (channelId: string) => {
    if (!device) return
    setAddingChannelId(channelId)
    try {
      await addCameraMutation.mutateAsync({
        deviceId: device.deviceId,
        channelId,
      })
      toast.success(t('channels.addSuccess', { channelId }))
    } catch (err) {
      toast.error(describeGb28181Error(err, t) || t('channels.addFailed'))
    } finally {
      setAddingChannelId(null)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex w-full flex-col sm:max-w-xl md:max-w-2xl'
      >
        <SheetHeader className='border-b pb-4'>
          <div className='flex items-center justify-between pr-6'>
            <div className='flex items-center gap-2'>
              <Layers className='size-5 text-muted-foreground' />
              <SheetTitle>{t('channels.title')}</SheetTitle>
            </div>
            <Button
              variant='outline'
              size='sm'
              disabled={
                isOffline || syncMutation.isPending || channelsQuery.isFetching
              }
              onClick={handleSync}
              className='gap-1.5'
              aria-label={t('channels.sync')}
            >
              <RefreshCw
                className={`size-3.5 ${
                  syncMutation.isPending || channelsQuery.isFetching
                    ? 'animate-spin'
                    : ''
                }`}
              />
              <span>
                {syncMutation.isPending
                  ? t('channels.syncing')
                  : t('channels.sync')}
              </span>
            </Button>
          </div>
          <SheetDescription>
            {t('channels.description', { deviceId: device?.deviceId ?? '' })}
          </SheetDescription>
          {isOffline && (
            <Alert
              variant='default'
              className={cn('mt-2 py-2', statusLevelClass('warning'))}
            >
              <AlertCircle className='size-4' />
              <AlertDescription className='text-xs'>
                {t('channels.offlineHint')}
              </AlertDescription>
            </Alert>
          )}
        </SheetHeader>

        <div className='flex-1 overflow-y-auto py-4'>
          {channelsQuery.isLoading ? (
            <div className='space-y-3'>
              <Skeleton className='h-8 w-full' />
              <Skeleton className='h-12 w-full' />
              <Skeleton className='h-12 w-full' />
              <Skeleton className='h-12 w-full' />
            </div>
          ) : channelsQuery.isError ? (
            <Alert variant='destructive'>
              <AlertCircle className='size-4' />
              <AlertTitle>{t('channels.loadFailed')}</AlertTitle>
              <AlertDescription>
                {describeGb28181Error(channelsQuery.error, t)}
              </AlertDescription>
            </Alert>
          ) : channels.length === 0 ? (
            <div className='flex flex-col items-center justify-center py-12 text-center'>
              <Camera className='size-10 text-muted-foreground/40' />
              <h3 className='mt-3 text-sm font-medium'>
                {t('channels.empty')}
              </h3>
              <p className='mt-1 text-xs text-muted-foreground'>
                {t('channels.emptyDesc')}
              </p>
            </div>
          ) : (
            <div className='rounded-md border'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className='w-[180px]'>
                      {t('channels.columns.channelId')}
                    </TableHead>
                    <TableHead>{t('channels.columns.name')}</TableHead>
                    <TableHead className='w-[100px]'>
                      {t('channels.columns.resolution')}
                    </TableHead>
                    <TableHead className='w-[120px] text-right'>
                      {t('channels.columns.actions')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {channels.map((ch) => {
                    const hasCamera = !!ch.cameraId
                    const isAdding = addingChannelId === ch.channelId
                    const resolution =
                      ch.resolutionWidth && ch.resolutionHeight
                        ? `${ch.resolutionWidth}x${ch.resolutionHeight}`
                        : '-'

                    return (
                      <TableRow key={ch.channelId}>
                        <TableCell className='font-mono text-xs'>
                          {ch.channelId}
                        </TableCell>
                        <TableCell className='text-xs font-medium'>
                          {ch.name || ch.channelId}
                        </TableCell>
                        <TableCell className='text-xs text-muted-foreground'>
                          {resolution}
                        </TableCell>
                        <TableCell className='text-right'>
                          {hasCamera ? (
                            <Badge
                              variant='outline'
                              className={cn(
                                'gap-1.5 text-xs font-normal',
                                statusLevelClass('active')
                              )}
                            >
                              <span
                                className={cn(
                                  'inline-block size-1.5 rounded-full',
                                  statusDotClass('active')
                                )}
                              />
                              <span>{t('channels.added')}</span>
                            </Badge>
                          ) : (
                            <Button
                              variant='outline'
                              size='sm'
                              className='h-7 gap-1 text-xs'
                              disabled={isAdding}
                              onClick={() => handleAddAsCamera(ch.channelId)}
                            >
                              {isAdding ? (
                                <Loader2 className='size-3 animate-spin' />
                              ) : (
                                <Camera className='size-3' />
                              )}
                              <span>
                                {isAdding
                                  ? t('channels.adding')
                                  : t('channels.addAsCamera')}
                              </span>
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
