import { useMemo } from 'react'
import { Activity, Cpu, MemoryStick, Network, Thermometer } from 'lucide-react'
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
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { type HardwareHistoryPoint } from '../types'
import { formatRate } from '../utils/format'

interface TrendChartsProps {
  history: HardwareHistoryPoint[]
  npuSupported?: boolean
}

export function TrendCharts({
  history,
  npuSupported = true,
}: TrendChartsProps) {
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
    <Card className='gap-4 border-border/80 bg-card/75 p-4.5 shadow-xs backdrop-blur-md transition-all'>
      <Tabs defaultValue='compute' className='w-full'>
        <CardHeader className='flex flex-col gap-3 p-0 sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex items-center gap-2.5'>
            <div className='flex size-7 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary shadow-2xs'>
              <Activity className='size-3.5' />
            </div>
            <div className='flex items-center gap-2'>
              <CardTitle className='text-sm font-semibold tracking-tight text-foreground'>
                {t('charts.title', {
                  defaultValue: '短期时序走势',
                })}
              </CardTitle>
              {chartData.length > 0 && (
                <Badge
                  variant='outline'
                  className='h-5 border-border/60 bg-muted/30 px-1.5 py-0 font-mono text-[10px] text-muted-foreground'
                >
                  {chartData.length} pts
                </Badge>
              )}
            </div>
          </div>

          <TabsList className='grid grid-cols-2 rounded-lg bg-muted/60 p-1 sm:flex sm:h-8'>
            <TabsTrigger
              value='compute'
              className='h-6 gap-1 px-2.5 text-xs font-medium'
            >
              <Cpu className='size-3' />
              <span>
                {t('charts.tabCompute', { defaultValue: '算力 (CPU/NPU)' })}
              </span>
            </TabsTrigger>
            <TabsTrigger
              value='memory'
              className='h-6 gap-1 px-2.5 text-xs font-medium'
            >
              <MemoryStick className='size-3' />
              <span>{t('charts.tabMemory', { defaultValue: '内存走势' })}</span>
            </TabsTrigger>
            <TabsTrigger
              value='network'
              className='h-6 gap-1 px-2.5 text-xs font-medium'
            >
              <Network className='size-3' />
              <span>
                {t('charts.tabNetwork', { defaultValue: '网络吞吐' })}
              </span>
            </TabsTrigger>
            <TabsTrigger
              value='temperature'
              className='h-6 gap-1 px-2.5 text-xs font-medium'
            >
              <Thermometer className='size-3' />
              <span>
                {t('charts.tabTemperature', { defaultValue: '芯片温度' })}
              </span>
            </TabsTrigger>
          </TabsList>
        </CardHeader>

        <CardContent className='p-0 pt-4'>
          {chartData.length === 0 ? (
            <div className='flex h-[240px] items-center justify-center rounded-lg border border-dashed border-border/60 bg-muted/20 text-xs text-muted-foreground'>
              {t('charts.emptyHistory', {
                defaultValue: '历史时序数据采集积累中...',
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
                      className='stroke-border/30'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[0, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      tickFormatter={(v) => `${v}%`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.1)',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}%`,
                      ]}
                    />
                    <Legend
                      verticalAlign='top'
                      align='right'
                      iconType='circle'
                      wrapperStyle={{ fontSize: '11px', top: -10 }}
                    />
                    <Line
                      type='monotone'
                      dataKey='cpu'
                      name={t('charts.cpuLoad', {
                        defaultValue: 'CPU 总体负载',
                      })}
                      stroke='#0ea5e9'
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                    {npuSupported && (
                      <Line
                        type='monotone'
                        dataKey='npu'
                        name={t('charts.npuLoad', {
                          defaultValue: 'NPU 算力负载',
                        })}
                        stroke='#f59e0b'
                        strokeWidth={2}
                        dot={false}
                        isAnimationActive={false}
                      />
                    )}
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
                          stopColor='#a855f7'
                          stopOpacity={0.35}
                        />
                        <stop
                          offset='95%'
                          stopColor='#a855f7'
                          stopOpacity={0.0}
                        />
                      </linearGradient>
                    </defs>
                    <CartesianGrid
                      strokeDasharray='3 3'
                      className='stroke-border/30'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[0, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      tickFormatter={(v) => `${v}%`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.1)',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}%`,
                      ]}
                    />
                    <Area
                      type='monotone'
                      dataKey='memory'
                      name={t('charts.memoryUsage', {
                        defaultValue: '物理内存占用率',
                      })}
                      stroke='#a855f7'
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
                          stopOpacity={0.35}
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
                          stopOpacity={0.35}
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
                      className='stroke-border/30'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      tickFormatter={(v) => formatRate(v)}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.1)',
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
                      wrapperStyle={{ fontSize: '11px', top: -10 }}
                    />
                    <Area
                      type='monotone'
                      dataKey='netRx'
                      name={t('charts.rxRate', {
                        defaultValue: '下行吞吐 (Rx)',
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
                      name={t('charts.txRate', {
                        defaultValue: '上行吞吐 (Tx)',
                      })}
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
                      className='stroke-border/30'
                    />
                    <XAxis
                      dataKey='time'
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      interval='preserveStartEnd'
                    />
                    <YAxis
                      domain={[20, 100]}
                      tickLine={false}
                      axisLine={false}
                      className='fill-muted-foreground font-mono text-[10px]'
                      tickFormatter={(v) => `${v}°C`}
                    />
                    <Tooltip
                      contentStyle={{
                        backgroundColor: 'var(--popover)',
                        color: 'var(--popover-foreground)',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        fontSize: '12px',
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.1)',
                      }}
                      formatter={(value: unknown) => [
                        `${Number(value).toFixed(1)}°C`,
                      ]}
                    />
                    <Line
                      type='monotone'
                      dataKey='temp'
                      name={t('charts.temperature', {
                        defaultValue: '芯片最高温',
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
