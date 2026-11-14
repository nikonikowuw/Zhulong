import { Loader2, MapPin, Route, Save, Undo2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { type SpatialMode } from '../../hooks/use-calibration-draft'
import { MapSwitcher } from './map-switcher'

interface SpatialWorkbenchHeaderProps {
  hasCurrentMap: boolean
  selectedMapId: string | null
  mode: SpatialMode
  isCalibrating: boolean
  hasUnsavedChanges: boolean
  isSavingPositions: boolean
  onSelectMapId: (id: string | null) => void
  onOpenUpload: () => void
  onRequestMode: (mode: SpatialMode) => void
  onSavePositions: () => void
  onResetDraft: () => void
}

export function SpatialWorkbenchHeader({
  hasCurrentMap,
  selectedMapId,
  mode,
  isCalibrating,
  hasUnsavedChanges,
  isSavingPositions,
  onSelectMapId,
  onOpenUpload,
  onRequestMode,
  onSavePositions,
  onResetDraft,
}: SpatialWorkbenchHeaderProps) {
  const { t } = useTranslation('spatial')

  return (
    <div className='flex flex-wrap items-center justify-between gap-2 sm:gap-4'>
      <div className='flex flex-wrap items-center gap-3 sm:gap-4'>
        <div>
          <h2 className='text-xl font-bold tracking-tight sm:text-2xl'>
            {t('title')}
          </h2>
          <p className='text-xs text-muted-foreground'>{t('subtitle')}</p>
        </div>

        <MapSwitcher
          selectedMapId={selectedMapId}
          onSelectMapId={onSelectMapId}
          onOpenUpload={onOpenUpload}
          disabled={isCalibrating && hasUnsavedChanges}
        />
      </div>

      <div className='flex items-center gap-2'>
        {hasCurrentMap && (
          <Tabs
            value={mode}
            onValueChange={(val) => onRequestMode(val as SpatialMode)}
          >
            <TabsList className='h-9'>
              <TabsTrigger value='trajectory' className='gap-1.5 text-xs'>
                <Route className='h-3.5 w-3.5' />
                {t('workbench.modeTrajectory')}
              </TabsTrigger>
              <TabsTrigger value='calibration' className='gap-1.5 text-xs'>
                <MapPin className='h-3.5 w-3.5' />
                {t('calibration.enter')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        )}

        {isCalibrating && (
          <>
            <Button
              size='sm'
              className='h-9 gap-1.5'
              onClick={onSavePositions}
              disabled={!hasUnsavedChanges || isSavingPositions}
            >
              {isSavingPositions ? (
                <Loader2 className='h-3.5 w-3.5 animate-spin' />
              ) : (
                <Save className='h-3.5 w-3.5' />
              )}
              {t('calibration.save')}
            </Button>

            {hasUnsavedChanges && (
              <Button
                variant='outline'
                size='sm'
                className='h-9 gap-1 text-muted-foreground'
                onClick={onResetDraft}
              >
                <Undo2 className='h-3.5 w-3.5' />
                {t('calibration.reset')}
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
