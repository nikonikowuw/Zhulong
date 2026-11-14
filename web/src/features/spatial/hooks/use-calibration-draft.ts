import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { type StampingCamera } from '../components/canvas/spatial-canvas'
import { type CameraPositionInfo } from '../data/schema'
import { useSaveCameraPositions } from './use-camera-positions'

export type SpatialMode = 'trajectory' | 'calibration'

interface UseCalibrationDraftOptions {
  mapId: string | null
  serverPositions: CameraPositionInfo[]
}

export function useCalibrationDraft({
  mapId,
  serverPositions,
}: UseCalibrationDraftOptions) {
  const { t } = useTranslation('spatial')
  const { mutateAsync: savePositions, isPending: isSavingPositions } =
    useSaveCameraPositions()

  const [mode, setMode] = useState<SpatialMode>('trajectory')
  const [pendingMode, setPendingMode] = useState<SpatialMode | null>(null)
  const [discardDialogOpen, setDiscardDialogOpen] = useState(false)

  /** 本地编辑中的点位；null 表示未修改，直接展示服务端数据。 */
  const [customPositions, setCustomPositions] = useState<
    CameraPositionInfo[] | null
  >(null)
  const [stampingCamera, setStampingCamera] = useState<StampingCamera | null>(
    null
  )

  // 用 ref 保存最新服务端点位，确保标定回调保持稳定引用，不随草稿更新重建。
  const serverPositionsRef = useRef(serverPositions)
  useEffect(() => {
    serverPositionsRef.current = serverPositions
  }, [serverPositions])

  const displayedPositions = customPositions ?? serverPositions
  const hasUnsavedChanges = customPositions !== null

  const resetDraft = useCallback(() => {
    setCustomPositions(null)
    setStampingCamera(null)
  }, [])

  const applyPositions = useCallback(
    (updater: (base: CameraPositionInfo[]) => CameraPositionInfo[]) => {
      setCustomPositions((prev) => updater(prev ?? serverPositionsRef.current))
    },
    []
  )

  const handleRequestMode = useCallback(
    (nextMode: SpatialMode) => {
      if (mode === nextMode) return
      if (mode === 'calibration' && hasUnsavedChanges) {
        setPendingMode(nextMode)
        setDiscardDialogOpen(true)
        return
      }
      setStampingCamera(null)
      setMode(nextMode)
    },
    [mode, hasUnsavedChanges]
  )

  const handleConfirmDiscard = useCallback(() => {
    resetDraft()
    setDiscardDialogOpen(false)
    if (pendingMode) {
      setMode(pendingMode)
      setPendingMode(null)
    } else {
      setMode('trajectory')
    }
  }, [pendingMode, resetDraft])

  const handleCancelDiscard = useCallback(() => {
    setPendingMode(null)
    setDiscardDialogOpen(false)
  }, [])

  const handleDiscardDialogOpenChange = useCallback((open: boolean) => {
    setDiscardDialogOpen(open)
    if (!open) {
      setPendingMode(null)
    }
  }, [])

  const handleDropCamera = useCallback(
    (cameraId: string, cameraName: string, x: number, y: number) => {
      applyPositions((base) => [
        ...base.filter((pos) => pos.cameraId !== cameraId),
        { cameraId, cameraName, x, y, angle: 90 },
      ])
      setStampingCamera(null)
    },
    [applyPositions]
  )

  const handleStampCamera = useCallback(
    (x: number, y: number) => {
      if (!stampingCamera) return
      handleDropCamera(stampingCamera.cameraId, stampingCamera.cameraName, x, y)
    },
    [stampingCamera, handleDropCamera]
  )

  const handleMoveCamera = useCallback(
    (cameraId: string, x: number, y: number) => {
      applyPositions((base) =>
        base.map((pos) => (pos.cameraId === cameraId ? { ...pos, x, y } : pos))
      )
    },
    [applyPositions]
  )

  const handleAngleChangeCamera = useCallback(
    (cameraId: string, angle: number) => {
      applyPositions((base) =>
        base.map((pos) => (pos.cameraId === cameraId ? { ...pos, angle } : pos))
      )
    },
    [applyPositions]
  )

  const handleRemoveCamera = useCallback(
    (cameraId: string) => {
      applyPositions((base) => base.filter((pos) => pos.cameraId !== cameraId))
    },
    [applyPositions]
  )

  const handleSavePositions = useCallback(async () => {
    if (!mapId || !customPositions) return
    try {
      await savePositions({
        mapId,
        positions: customPositions.map((pos) => ({
          cameraId: pos.cameraId,
          x: pos.x,
          y: pos.y,
          angle: pos.angle,
        })),
      })
      setCustomPositions(null)
      toast.success(t('calibration.saveSuccess'))
    } catch (err: unknown) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t('calibration.saveFailed')
      )
    }
  }, [mapId, customPositions, savePositions, t])

  return {
    mode,
    isCalibrating: mode === 'calibration',
    displayedPositions,
    hasUnsavedChanges,
    stampingCamera,
    setStampingCamera,
    discardDialogOpen,
    isSavingPositions,
    resetDraft,
    handleRequestMode,
    handleConfirmDiscard,
    handleCancelDiscard,
    handleDiscardDialogOpenChange,
    handleDropCamera,
    handleStampCamera,
    handleMoveCamera,
    handleAngleChangeCamera,
    handleRemoveCamera,
    handleSavePositions,
  }
}
