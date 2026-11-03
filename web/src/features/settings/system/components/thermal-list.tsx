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
    <Card className='gap-3 p-4'>
      <CardHeader className='flex-row items-center justify-between p-0'>
        <CardTitle className='flex items-center gap-2 text-base font-semibold'>
          <Thermometer className='size-4 text-amber-500' />
          <span>
            {t('details.thermalTitle', {
              defaultValue: 'Thermal Sensors',
            })}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className='p-0 pt-2'>
        {thermals.length === 0 ? (
          <div className='py-4 text-center text-xs text-muted-foreground'>
            -
          </div>
        ) : (
          <div className='grid grid-cols-2 gap-3 sm:grid-cols-4 md:grid-cols-6'>
            {thermals.map((sensor, idx) => (
              <div
                key={`${sensor.label}-${idx}`}
                className='flex flex-col items-center justify-center gap-1 rounded-lg border bg-muted/30 p-2.5 text-center'
              >
                <span className='text-xs font-medium text-muted-foreground'>
                  {sensor.label}
                </span>
                <span className='font-mono text-base font-bold text-foreground'>
                  {sensor.temperature.toFixed(1)}°C
                </span>
                <Badge
                  variant='outline'
                  className={`px-1 py-0 text-[10px] ${
                    sensor.temperature >= 75
                      ? 'border-destructive text-destructive'
                      : sensor.temperature >= 60
                        ? 'border-amber-500 text-amber-500'
                        : 'border-emerald-500 text-emerald-500'
                  }`}
                >
                  {sensor.temperature >= 75
                    ? 'HOT'
                    : sensor.temperature >= 60
                      ? 'WARM'
                      : 'NORMAL'}
                </Badge>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
