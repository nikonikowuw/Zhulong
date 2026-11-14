import { Loader2, Map as MapIcon, Pencil, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import useDialogState from '@/hooks/use-dialog-state'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useDeleteMap, useSpatialMaps } from '../../hooks/use-spatial-maps'
import { MapRenameDialog } from './map-rename-dialog'
import { MapUploadDialog } from './map-upload-dialog'

type MapDialog = 'upload' | 'rename' | 'delete'

interface MapSwitcherProps {
  selectedMapId: string | null
  onSelectMapId: (id: string | null) => void
  disabled?: boolean
  onOpenUpload?: () => void
}

export function MapSwitcher({
  selectedMapId,
  onSelectMapId,
  disabled,
  onOpenUpload,
}: MapSwitcherProps) {
  const { t } = useTranslation('spatial')
  const { data: maps = [], isLoading } = useSpatialMaps()
  const deleteMutation = useDeleteMap()
  // 弹窗开合由 useDialogState 统一持有，不再层层透传 isOpen/setIsOpen。
  const [openDialog, setOpenDialog] = useDialogState<MapDialog>()

  const currentMap = maps.find((map) => map.id === selectedMapId)

  const handleDelete = async () => {
    if (!selectedMapId) return
    try {
      await deleteMutation.mutateAsync(selectedMapId)
      toast.success(t('map.deleteSuccess'))
      setOpenDialog(null)
      const remaining = maps.filter((map) => map.id !== selectedMapId)
      onSelectMapId(remaining[0]?.id ?? null)
    } catch (err: unknown) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t('map.deleteFailed')
      )
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <div className='w-48 sm:w-60'>
        <Select
          value={selectedMapId ?? ''}
          onValueChange={(val) => onSelectMapId(val || null)}
          disabled={disabled || isLoading || maps.length === 0}
        >
          <SelectTrigger
            className='h-9'
            aria-label={t('map.selectPlaceholder')}
          >
            <div className='flex items-center gap-2 truncate'>
              <MapIcon className='h-4 w-4 shrink-0 text-muted-foreground' />
              <SelectValue placeholder={t('map.selectPlaceholder')} />
            </div>
          </SelectTrigger>
          <SelectContent>
            {maps.map((map) => (
              <SelectItem key={map.id} value={map.id}>
                {map.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <Button
        variant='outline'
        size='sm'
        className='h-9'
        onClick={onOpenUpload ?? (() => setOpenDialog('upload'))}
        disabled={disabled}
      >
        <Plus className='me-1 h-4 w-4' />
        {t('map.uploadMap')}
      </Button>

      {currentMap && (
        <>
          <Button
            variant='ghost'
            size='icon'
            className='h-9 w-9 text-muted-foreground'
            onClick={() => setOpenDialog('rename')}
            disabled={disabled}
            aria-label={t('map.rename')}
            title={t('map.rename')}
          >
            <Pencil className='h-4 w-4' />
          </Button>
          <Button
            variant='ghost'
            size='icon'
            className='h-9 w-9 text-muted-foreground hover:text-destructive'
            onClick={() => setOpenDialog('delete')}
            disabled={disabled || deleteMutation.isPending}
            aria-label={t('map.delete')}
            title={t('map.delete')}
          >
            <Trash2 className='h-4 w-4' />
          </Button>
        </>
      )}

      {!onOpenUpload && (
        <MapUploadDialog
          open={openDialog === 'upload'}
          onOpenChange={(open) => setOpenDialog(open ? 'upload' : null)}
          onSuccess={(newId) => onSelectMapId(newId)}
        />
      )}

      {currentMap && (
        <MapRenameDialog
          mapId={currentMap.id}
          currentName={currentMap.name}
          open={openDialog === 'rename'}
          onOpenChange={(open) => setOpenDialog(open ? 'rename' : null)}
        />
      )}

      <AlertDialog
        open={openDialog === 'delete'}
        onOpenChange={(open) => {
          if (!deleteMutation.isPending) {
            setOpenDialog(open ? 'delete' : null)
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('map.deleteConfirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('map.deleteConfirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteMutation.isPending}>
              {t('common:actions.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              className='text-destructive-foreground bg-destructive hover:bg-destructive/90'
              disabled={deleteMutation.isPending}
              onClick={(e) => {
                e.preventDefault()
                void handleDelete()
              }}
            >
              {deleteMutation.isPending && (
                <Loader2 className='me-2 h-4 w-4 animate-spin' />
              )}
              {t('map.delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
