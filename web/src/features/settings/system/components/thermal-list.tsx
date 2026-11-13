import { Thermometer } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { STATUS_LEVEL_CLASS, type StatusLevel } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { type TemperatureInfo } from '../types'
import { getThermalBarClass, getThermalTextColor } from '../utils/format'

interface ThermalListProps {
  thermals: TemperatureInfo[]
}

/** 温度档位文案：状态语义，必须走 status-palette（§1.3.3）。 */
const THERMAL_LEVEL_KEY: Record<StatusLevel, [string, string]> = {
  active: ['details.thermalNormal', 'NORMAL'],
  warning: ['details.thermalWarm', 'WARM'],
  destructive: ['details.thermalHot', 'HOT'],
  inactive: ['details.thermalNormal', 'NORMAL'],
}

function thermalLevelOf(temperature: number): StatusLevel {
  if (temperature >= 75) return 'destructive'
  if (temperature >= 60) return 'warning'
  return 'active'
}

export function ThermalList({ thermals }: ThermalListProps) {
  const { t } = useTranslation('system')

  return (
    <Card className='gap-3 border-border/80 bg-card/75 p-5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-8 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/40 text-muted-foreground'>
            <Thermometer className='size-4' />
          </div>
          <div className='space-y-0.5'>
            <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
              {t('details.thermalTitle', {
                defaultValue: '各芯片与测温传感器点位',
              })}
            </CardTitle>
            <p className='text-xs text-muted-foreground'>
              {t('details.thermalSubtitle', {
                defaultValue: '阈值：≥60°C 警戒、≥75°C 过热',
              })}
            </p>
          </div>
        </div>
        {thermals.length > 0 && (
          <Badge
            variant='outline'
            className='h-5 shrink-0 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
          >
            {t('details.thermalCount', {
              count: thermals.length,
              defaultValue: `${thermals.length} 测温点位`,
            })}
          </Badge>
        )}
      </CardHeader>
      <CardContent className='p-0 pt-3'>
        {thermals.length === 0 ? (
          <div className='py-6 text-center text-xs text-muted-foreground'>
            {t('details.thermalEmpty', {
              defaultValue: '未检出测温传感器',
            })}
          </div>
        ) : (
          <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
            {thermals.map((sensor, idx) => {
              const level = thermalLevelOf(sensor.temperature)
              const [labelKey, labelFallback] = THERMAL_LEVEL_KEY[level]
              return (
                <div
                  key={`${sensor.label}-${idx}`}
                  className='flex flex-col gap-2 rounded-lg border border-border/60 bg-muted/20 p-3 transition-colors hover:bg-muted/40'
                >
                  <div className='flex items-center justify-between gap-1'>
                    <span className='truncate font-mono text-xs font-semibold text-muted-foreground'>
                      {sensor.label}
                    </span>
                    <Badge
                      variant='outline'
                      className={cn(
                        'h-4 shrink-0 px-1 py-0 font-mono text-[9px] font-semibold',
                        STATUS_LEVEL_CLASS[level]
                      )}
                    >
                      {t(labelKey, { defaultValue: labelFallback })}
                    </Badge>
                  </div>

                  <span
                    className={cn(
                      'font-mono text-lg font-bold tracking-tight tabular-nums',
                      getThermalTextColor(sensor.temperature)
                    )}
                  >
                    {sensor.temperature.toFixed(1)}°C
                  </span>

                  {/* 微型标尺（0-100°C） */}
                  <div className='h-1 w-full overflow-hidden rounded-full bg-muted/80'>
                    <div
                      className={cn(
                        'h-full transition-all duration-500',
                        getThermalBarClass(sensor.temperature)
                      )}
                      style={{
                        width: `${Math.min(
                          100,
                          Math.max(5, sensor.temperature)
                        )}%`,
                      }}
                    />
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
