import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  AlertCircle,
  Camera,
  Check,
  Loader2,
  Radar,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { camerasApi } from '../api/cameras-api'
import { type DiscoveredDevice } from '../data/schema'
import { useCamerasContext } from './cameras-provider'

function useScanCountdown(isScanning: boolean, durationSeconds = 3) {
  const [remaining, setRemaining] = useState(durationSeconds)

  useEffect(() => {
    if (!isScanning) return
    const deadline = Date.now() + durationSeconds * 1000
    const timer = window.setInterval(() => {
      const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
      setRemaining(left)
    }, 250)
    return () => {
      window.clearInterval(timer)
    }
  }, [isScanning, durationSeconds])

  return isScanning ? remaining : durationSeconds
}

export function CamerasDiscoverDialog() {
  const { t } = useTranslation('cameras')
  const { open, setOpen, setCurrentRow, setDiscoveredDevice } =
    useCamerasContext()

  const isOpen = open === 'discover'

  const {
    data: devices = [],
    isFetching: isLoading,
    error: queryError,
    refetch,
    isFetched: hasScanned,
  } = useQuery({
    queryKey: ['cameras-discover'],
    queryFn: () => camerasApi.discoverCameras(3),
    enabled: isOpen,
    staleTime: 5000,
    retry: false,
  })

  const countdown = useScanCountdown(isLoading, 3)

  const error = queryError
    ? queryError instanceof Error
      ? queryError.message
      : t('table.loadError')
    : null

  const handleUseDevice = (device: DiscoveredDevice) => {
    setCurrentRow(null)
    setDiscoveredDevice(device)
    setOpen('create')
    toast.info(t('discover.prefilledTip', { ip: device.ip }))
  }

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(val) => {
        if (!isLoading && !val) {
          setOpen(null)
        }
      }}
    >
      <DialogContent className='w-full overflow-hidden sm:max-w-2xl'>
        <DialogHeader>
          <div className='flex items-center gap-2'>
            <div className='flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary'>
              <Radar className='size-5' />
            </div>
            <div className='min-w-0'>
              <DialogTitle>{t('discover.title')}</DialogTitle>
              <DialogDescription className='mt-1 text-xs sm:text-sm'>
                {t('discover.description')}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className='min-w-0 space-y-4 py-2'>
          <div className='flex flex-wrap items-center justify-between gap-2 border-b pb-3'>
            <div className='text-sm text-muted-foreground'>
              {isLoading
                ? `${t('discover.scanning')} (${countdown}s)`
                : hasScanned
                  ? t('discover.scanComplete', { count: devices.length })
                  : ''}
            </div>
            <Button
              size='sm'
              variant='outline'
              disabled={isLoading}
              onClick={() => void refetch()}
            >
              <RefreshCw
                className={`me-1.5 size-3.5 ${isLoading ? 'animate-spin' : ''}`}
              />
              {isLoading ? t('discover.scanning') : t('discover.startScan')}
            </Button>
          </div>

          {error ? (
            <div className='flex items-center gap-2 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive'>
              <AlertCircle className='size-4 shrink-0' />
              <span>{error}</span>
            </div>
          ) : null}

          <div className='max-h-[55vh] space-y-2.5 overflow-y-auto pe-1'>
            {isLoading && devices.length === 0 ? (
              <div className='flex flex-col items-center justify-center py-12 text-center text-muted-foreground'>
                <Loader2 className='size-8 animate-spin text-primary' />
                <p className='mt-3 text-sm'>{t('discover.scanning')}</p>
              </div>
            ) : null}

            {!isLoading && hasScanned && devices.length === 0 ? (
              <div className='flex flex-col items-center justify-center py-10 text-center'>
                <div className='flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground'>
                  <Camera className='size-6' />
                </div>
                <h4 className='mt-3 font-medium'>{t('discover.noDevices')}</h4>
                <p className='mt-1 max-w-sm text-xs text-muted-foreground'>
                  {t('discover.noDevicesDesc')}
                </p>
                <Button
                  variant='outline'
                  size='sm'
                  className='mt-4'
                  onClick={() => void refetch()}
                >
                  <RefreshCw className='me-1.5 size-3.5' />
                  {t('table.retry')}
                </Button>
              </div>
            ) : null}

            {devices.map((device) => (
              <div
                key={device.id || device.ip}
                className='flex flex-col gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/40 sm:flex-row sm:items-center sm:justify-between'
              >
                <div className='min-w-0 flex-1 space-y-1 overflow-hidden'>
                  <div className='flex items-center gap-2'>
                    <span className='truncate text-sm font-medium'>
                      {device.name || device.model || `Camera-${device.ip}`}
                    </span>
                    {device.manufacturer ? (
                      <Badge
                        variant='secondary'
                        className='text-[11px] font-normal'
                      >
                        {device.manufacturer}
                      </Badge>
                    ) : null}
                    <Badge
                      variant='outline'
                      className='text-[11px] font-normal'
                    >
                      ONVIF
                    </Badge>
                  </div>
                  <div className='flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground'>
                    <span>
                      {t('discover.ip')}: {device.ip}:{device.port}
                    </span>
                    {device.model ? (
                      <span>
                        {t('discover.model')}: {device.model}
                      </span>
                    ) : null}
                  </div>
                  {device.suggestedRtspUrl ? (
                    <div className='truncate font-mono text-[11px] text-muted-foreground/80'>
                      {device.suggestedRtspUrl}
                    </div>
                  ) : null}
                </div>

                <Button
                  size='sm'
                  data-testid='use-device-btn'
                  onClick={() => handleUseDevice(device)}
                  className='shrink-0'
                >
                  <Check className='me-1 size-3.5' />
                  <span>{t('discover.useDevice')}</span>
                </Button>
              </div>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
