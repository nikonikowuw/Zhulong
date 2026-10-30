import { Sliders, Video, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_LEVEL_CLASS, statusDotClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { type Camera as CameraSchema } from '@/features/cameras/data/schema'
import { MAX_COMPUTE_POINTS } from '../hooks/use-task-compute-budget'
import { ChannelTaskBudgetMeter } from './channel-task-budget-meter'

export interface ChannelTaskCameraConfig {
  cameraId: string
  cameras: CameraSchema[]
  /** 从某行「配置」进入时相机固定，不再展示下拉。 */
  fixedCameraName?: string
  isCameraOnline: boolean
  onCameraChange: (cameraId: string) => void
}

export interface ChannelTaskBudgetConfig {
  channelPoints: number
  projectedPoints: number
  isOverBudget: boolean
}

export interface ChannelTaskSwitchConfig {
  channelEnabled: boolean
  onChannelEnabledChange: (enabled: boolean) => void
  /** 已勾选但处于 DISABLED 状态的算法数量：>0 时才提供「保存时自动激活」。 */
  disabledAlgoCount: number
  autoEnableDisabled: boolean
  onAutoEnableChange: (autoEnable: boolean) => void
}

export interface ChannelTaskDialogHeaderProps {
  camera: ChannelTaskCameraConfig
  budget: ChannelTaskBudgetConfig
  switches: ChannelTaskSwitchConfig
  onClose?: () => void
}

/** 顶部控制条：通道标识、算力占用、通道总开关与自动激活选项。 */
export function ChannelTaskDialogHeader({
  camera,
  budget,
  switches,
  onClose,
}: ChannelTaskDialogHeaderProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const { cameraId, cameras, fixedCameraName, isCameraOnline, onCameraChange } =
    camera
  const { channelPoints, projectedPoints, isOverBudget } = budget
  const {
    channelEnabled,
    onChannelEnabledChange,
    disabledAlgoCount,
    autoEnableDisabled,
    onAutoEnableChange,
  } = switches
  const selectedCamera = cameras.find((item) => item.id === cameraId)

  return (
    <DialogHeader className='border-b border-border bg-card px-6 py-3.5'>
      <div className='flex items-center justify-between gap-4'>
        <div className='flex items-center gap-3'>
          <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-muted/60 text-muted-foreground'>
            <Sliders className='size-4' />
          </div>
          <div className='flex flex-col gap-0.5'>
            <div className='flex items-center gap-2'>
              <DialogTitle className='text-sm font-semibold text-foreground'>
                {t('dialog.title', '配置通道算法任务')}
              </DialogTitle>
              <DialogDescription className='sr-only'>
                {t(
                  'dialog.desc',
                  '为摄像头选择并发执行的算法包，独立设置采样帧率与运行参数。'
                )}
              </DialogDescription>
              {selectedCamera && (
                <Badge
                  variant='outline'
                  className={cn(
                    'gap-1 px-1.5 py-0 text-[10px] font-normal',
                    isCameraOnline
                      ? STATUS_LEVEL_CLASS.active
                      : STATUS_LEVEL_CLASS.inactive
                  )}
                >
                  <span
                    className={cn(
                      'size-1.5 rounded-full',
                      statusDotClass(isCameraOnline ? 'active' : 'inactive')
                    )}
                  />
                  {isCameraOnline
                    ? t('channelCard.cameraOnline', '在线')
                    : t('channelCard.cameraOffline', '离线')}
                </Badge>
              )}
            </div>
            <div className='flex items-center gap-2 text-xs text-muted-foreground'>
              <span className='font-semibold'>
                {t('dialog.selectCamera', '目标摄像头')}:
              </span>
              {fixedCameraName ? (
                <span className='font-medium text-foreground'>
                  {fixedCameraName}
                </span>
              ) : (
                <Select value={cameraId} onValueChange={onCameraChange}>
                  <SelectTrigger className='h-6 w-44 bg-background text-xs'>
                    <SelectValue
                      placeholder={t(
                        'dialog.cameraPlaceholder',
                        '请选择摄像头'
                      )}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {cameras.map((camera) => (
                      <SelectItem key={camera.id} value={camera.id}>
                        {camera.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              <span className='text-border'>•</span>
              <span className='inline-flex items-center gap-1'>
                <Video className='size-3' />
                {selectedCamera?.streams?.[0]?.protocol?.toUpperCase() ||
                  'RTSP'}{' '}
                ·{' '}
                {selectedCamera?.streams?.[0]?.role === 'main'
                  ? t('channelCard.mainStream', '主码流')
                  : t('channelCard.subStream', '子码流')}
              </span>
            </div>
          </div>
        </div>

        <ChannelTaskBudgetMeter
          channelPoints={channelPoints}
          projectedPoints={projectedPoints}
          maxPoints={MAX_COMPUTE_POINTS}
          isOverBudget={isOverBudget}
        />

        <div className='flex items-center gap-3'>
          <div className='flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-1'>
            <Label
              htmlFor='channel-enabled-switch'
              className='cursor-pointer text-xs font-medium text-foreground'
            >
              {t('dialog.channelEnabledLabel', '通道分析总开关')}
            </Label>
            <Switch
              id='channel-enabled-switch'
              checked={channelEnabled}
              onCheckedChange={onChannelEnabledChange}
            />
          </div>

          {disabledAlgoCount > 0 && (
            <div className='flex items-center gap-2'>
              <Checkbox
                id='auto-enable-chk'
                checked={autoEnableDisabled}
                onCheckedChange={(checked) =>
                  onAutoEnableChange(Boolean(checked))
                }
              />
              <Label
                htmlFor='auto-enable-chk'
                className='cursor-pointer text-xs font-normal text-muted-foreground'
              >
                {t(
                  'dialog.autoEnableDisabledAlgos',
                  '保存时自动激活所选算法（立即启动推理）'
                )}
              </Label>
            </div>
          )}

          {onClose && (
            <>
              <Separator orientation='vertical' className='h-5' />
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-8 rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground'
                onClick={onClose}
                aria-label={t('dialog.close', '关闭')}
                title={t('dialog.close', '关闭')}
              >
                <X className='size-4' />
              </Button>
            </>
          )}
        </div>
      </div>
    </DialogHeader>
  )
}
