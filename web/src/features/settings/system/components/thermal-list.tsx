import { Thermometer } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { type TemperatureInfo } from '../types'

interface ThermalListProps {
  thermals: TemperatureInfo[]
}

export function ThermalList({ thermals }: ThermalListProps) {
  const { t } = useTranslation('system')

  return (
    <Card className='gap-3 border-border/80 bg-card/75 p-4.5 shadow-xs backdrop-blur-md transition-all'>
      <CardHeader className='flex-row items-center justify-between space-y-0 p-0'>
        <div className='flex items-center gap-2.5'>
          <div className='flex size-7 shrink-0 items-center justify-center rounded-lg border border-amber-500/20 bg-amber-500/10 text-amber-500 shadow-2xs'>
            <Thermometer className='size-3.5' />
          </div>
          <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
            {t('details.thermalTitle', {
              defaultValue: '各芯片与测温传感器点位',
            })}
          </CardTitle>
        </div>
        {thermals.length > 0 && (
          <div className='flex items-center gap-2'>
            <Badge
              variant='outline'
              className='h-5 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
            >
              {thermals.length} 测温点位
            </Badge>
          </div>
        )}
      </CardHeader>
      <CardContent className='p-0 pt-3'>
        {thermals.length === 0 ? (
          <div className='py-6 text-center text-xs text-muted-foreground'>
            未检出测温传感器
          </div>
        ) : (
          <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
            {thermals.map((sensor, idx) => {
              const isHot = sensor.temperature >= 75
              const isWarm = sensor.temperature >= 60 && !isHot
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
                      className={`h-4 px-1 py-0 font-mono text-[9px] font-semibold ${
                        isHot
                          ? 'border-destructive/40 bg-destructive/10 text-destructive'
                          : isWarm
                            ? 'border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400'
                            : 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                      }`}
                    >
                      {isHot ? 'HOT' : isWarm ? 'WARM' : 'NORMAL'}
                    </Badge>
                  </div>

                  <div className='flex items-baseline justify-between'>
                    <span
                      className={`font-mono text-lg font-bold tracking-tight ${
                        isHot
                          ? 'text-destructive'
                          : isWarm
                            ? 'text-amber-500'
                            : 'text-foreground'
                      }`}
                    >
                      {sensor.temperature.toFixed(1)}°C
                    </span>
                  </div>

                  {/* Mini Thermal Bar (0-100°C) */}
                  <div className='h-1 w-full overflow-hidden rounded-full bg-muted/80'>
                    <div
                      className={`h-full transition-all duration-500 ${
                        isHot
                          ? 'bg-destructive'
                          : isWarm
                            ? 'bg-amber-500'
                            : 'bg-emerald-500'
                      }`}
                      style={{
                        width: `${Math.min(
                          100,
                          Math.max(5, (sensor.temperature / 100) * 100)
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
