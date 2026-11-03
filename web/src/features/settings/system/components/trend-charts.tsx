import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { type HardwareHistoryPoint } from '../types'
import { formatRate } from '../utils/format'

interface TrendChartsProps {
  history: HardwareHistoryPoint[]
}

export function TrendCharts({ history }: TrendChartsProps) {
  const { t } = useTranslation('system')

  const chartData = useMemo(() => {
    return history.map((pt) => {
      const d = new Date(pt.timestamp)
      const timeStr = `${d.getHours().toString().padStart(2, '0')}:${d
        .getMinutes()
        .toString()
        .padStart(2, '0')}:${d.getSeconds().toString().padStart(2, '0')}`
      return {
        time: timeStr,
        cpu: pt.cpuUsagePercent,
        memory: pt.memUsagePercent,
        npu: pt.npuLoadPercent,
        netRx: pt.netRxRate,
        netTx: pt.netTxRate,
        temp: pt.temperature,
      }
    })
  }, [history])

  return (
    <Card className='gap-4 p-4'>
      <Tabs defaultValue='compute' className='w-full'>
        <CardHeader className='flex flex-col gap-2 p-0 sm:flex-row sm:items-center sm:justify-between'>
          <CardTitle className='text-base font-semibold'>
            {t('charts.title', {
              defaultValue: 'Short-term Historical Trends',
            })}
          </CardTitle>
          <TabsList className='grid grid-cols-2 sm:flex sm:h-8'>
            <TabsTrigger value='compute' className='text-xs'>
              {t('charts.tabCompute', { defaultValue: 'Compute (CPU/NPU)' })}
            </TabsTrigger>
            <TabsTrigger value='memory' className='text-xs'>
              {t('charts.tabMemory', { defaultValue: 'Memory' })}
            </TabsTrigger>
            <TabsTrigger value='network' className='text-xs'>
              {t('charts.tabNetwork', { defaultValue: 'Network' })}
            </TabsTrigger>
            <TabsTrigger value='temperature' className='text-xs'>
              {t('charts.tabTemperature', { defaultValue: 'Thermal' })}
            </TabsTrigger>
          </TabsList>
        </CardHeader>

        <CardContent className='p-0 pt-3'>
          {chartData.length === 0 ? (
            <div className='flex h-[240px] items-center justify-center text-sm text-muted-foreground'>
              {t('charts.emptyHistory', {
                defaultValue: 'Accumulating telemetry history...',
              })}
            </div>
          ) : (
            <>
              {/* 1. Compute Tab (CPU & NPU) */}
              <TabsContent value='compute' className='mt-0 h-[240px] w-full'>
                <ResponsiveContainer width='100%' height='100%'>
                  <LineChart
                    data={chartData}
                    margin={{ top: 10, right: 10, left: -20, bottom: 0 }}
                  >
                    <CartesianGrid
                      strokeDasharray='3 3'
                      className='stroke-border/40'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[0, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      tickFormatter={(v) => `${v}%`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}%`,
                      ]}
                    />
                    <Legend
                      verticalAlign='top'
                      align='right'
                      iconType='circle'
                      wrapperStyle={{ fontSize: '11px', top: -8 }}
                    />
                    <Line
                      type='monotone'
                      dataKey='cpu'
                      name={t('charts.cpuLoad', { defaultValue: 'CPU Load' })}
                      stroke='#3b82f6'
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                    <Line
                      type='monotone'
                      dataKey='npu'
                      name={t('charts.npuLoad', { defaultValue: 'NPU Load' })}
                      stroke='#10b981'
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </TabsContent>

              {/* 2. Memory Tab */}
              <TabsContent value='memory' className='mt-0 h-[240px] w-full'>
                <ResponsiveContainer width='100%' height='100%'>
                  <AreaChart
                    data={chartData}
                    margin={{ top: 10, right: 10, left: -20, bottom: 0 }}
                  >
                    <defs>
                      <linearGradient id='memGrad' x1='0' y1='0' x2='0' y2='1'>
                        <stop
                          offset='5%'
                          stopColor='#8b5cf6'
                          stopOpacity={0.4}
                        />
                        <stop
                          offset='95%'
                          stopColor='#8b5cf6'
                          stopOpacity={0.0}
                        />
                      </linearGradient>
                    </defs>
                    <CartesianGrid
                      strokeDasharray='3 3'
                      className='stroke-border/40'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[0, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      tickFormatter={(v) => `${v}%`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}%`,
                      ]}
                    />
                    <Area
                      type='monotone'
                      dataKey='memory'
                      name={t('charts.memoryUsage', {
                        defaultValue: 'RAM Usage',
                      })}
                      stroke='#8b5cf6'
                      fillOpacity={1}
                      fill='url(#memGrad)'
                      strokeWidth={2}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </TabsContent>

              {/* 3. Network Tab */}
              <TabsContent value='network' className='mt-0 h-[240px] w-full'>
                <ResponsiveContainer width='100%' height='100%'>
                  <AreaChart
                    data={chartData}
                    margin={{ top: 10, right: 10, left: 10, bottom: 0 }}
                  >
                    <defs>
                      <linearGradient id='rxGrad' x1='0' y1='0' x2='0' y2='1'>
                        <stop
                          offset='5%'
                          stopColor='#0ea5e9'
                          stopOpacity={0.4}
                        />
                        <stop
                          offset='95%'
                          stopColor='#0ea5e9'
                          stopOpacity={0.0}
                        />
                      </linearGradient>
                      <linearGradient id='txGrad' x1='0' y1='0' x2='0' y2='1'>
                        <stop
                          offset='5%'
                          stopColor='#10b981'
                          stopOpacity={0.4}
                        />
                        <stop
                          offset='95%'
                          stopColor='#10b981'
                          stopOpacity={0.0}
                        />
                      </linearGradient>
                    </defs>
                    <CartesianGrid
                      strokeDasharray='3 3'
                      className='stroke-border/40'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      tickFormatter={(v) => formatRate(v)}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                      }}
                      formatter={(value: unknown, name?: unknown) => [
                        formatRate(Number(value)),
                        String(name ?? ''),
                      ]}
                    />
                    <Legend
                      verticalAlign='top'
                      align='right'
                      iconType='circle'
                      wrapperStyle={{ fontSize: '11px', top: -8 }}
                    />
                    <Area
                      type='monotone'
                      dataKey='netRx'
                      name={t('charts.rxRate', {
                        defaultValue: 'Download (Rx)',
                      })}
                      stroke='#0ea5e9'
                      fillOpacity={1}
                      fill='url(#rxGrad)'
                      strokeWidth={1.5}
                      isAnimationActive={false}
                    />
                    <Area
                      type='monotone'
                      dataKey='netTx'
                      name={t('charts.txRate', { defaultValue: 'Upload (Tx)' })}
                      stroke='#10b981'
                      fillOpacity={1}
                      fill='url(#txGrad)'
                      strokeWidth={1.5}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </TabsContent>

              {/* 4. Thermal Tab */}
              <TabsContent
                value='temperature'
                className='mt-0 h-[240px] w-full'
              >
                <ResponsiveContainer width='100%' height='100%'>
                  <LineChart
                    data={chartData}
                    margin={{ top: 10, right: 10, left: -20, bottom: 0 }}
                  >
                    <CartesianGrid
                      strokeDasharray='3 3'
                      className='stroke-border/40'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[20, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground text-[10px]'
                      tickFormatter={(v) => `${v}°C`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}°C`,
                      ]}
                    />
                    <Line
                      type='monotone'
                      dataKey='temp'
                      name={t('charts.temperature', {
                        defaultValue: 'Temperature',
                      })}
                      stroke='#f59e0b'
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </TabsContent>
            </>
          )}
        </CardContent>
      </Tabs>
    </Card>
  )
}
