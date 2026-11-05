import { useMemo, useState } from 'react'
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

/**
 * 绘图区四周的留白。
 *
 * `left` 必须 ≥ 0：这里放的是 Y 轴刻度文字的容器，任何负值都会把 `100%` 这类
 * 三位数刻度推出卡片左边缘（Recharts 不会为 margin 之外的内容做位移补偿）。
 * `top` 为顶部 Legend 行预留高度。
 * `right` 要容得下**最后一个时间标签的一半**（`20:01:27` 在 10px 下约 48px），
 * 否则它会从绘图区右端溢出去。
 * `bottom` 给 X 轴行留一点呼吸位，避免标签底边正好压在容器边缘上。
 */
const CHART_MARGIN = { top: 30, right: 32, left: 4, bottom: 6 } as const

/** Y 轴槽宽：按最宽刻度文字留够空间，宁可多一点也不要截断。 */
const PERCENT_AXIS_WIDTH = 46 // "100%"
const TEMP_AXIS_WIDTH = 56 // "100°C"
const RATE_AXIS_WIDTH = 78 // "1.2 MB/s"

/**
 * 轴刻度文字。
 *
 * ⚠️ 必须走 `tick` prop，**不能**靠 `className` 传 `text-[10px]`：Recharts v3 把
 * className 加在轴的 `<g>` 容器上，而 SVG `<text>` 会回落到浏览器默认的 16px，
 * 于是 `100%` 宽 38px 把 40px 的 Y 轴槽撑爆、时间标签也顶出 X 轴行高。
 */
const AXIS_TICK = {
  fontSize: 12,
  fontWeight: 500,
  fill: 'var(--muted-foreground)',
  // 带 fallback：若主题未注入 --font-mono，仍落到等宽族而不是浏览器默认衬线体。
  fontFamily: 'var(--font-mono, ui-monospace, monospace)',
} as const

const CHART_AREA_CLASS = 'mt-0 h-[240px] w-full'

/**
 * Recharts v3 的 `ResponsiveContainer` 初始尺寸默认是 `{width: -1, height: -1}`，
 * 首帧会往控制台刷一条 `width(-1) ... should be greater than 0` 的警告。
 * 给一个与真实布局接近的初值（240 高 - 28 顶留白 - 18 X 轴），
 * ResizeObserver 首次回调后就会被真实尺寸覆盖。
 */
const CHART_INITIAL_DIMENSION = { width: 640, height: 182 }

const TOOLTIP_STYLE = {
  backgroundColor: 'var(--popover)',
  color: 'var(--popover-foreground)',
  borderRadius: '8px',
  border: '1px solid var(--border)',
  fontSize: '12px',
} as const

const LEGEND_PROPS = {
  verticalAlign: 'top',
  align: 'right',
  iconType: 'circle',
  iconSize: 8,
  wrapperStyle: { fontSize: '12px', paddingBottom: '6px' },
} as const

export function TrendCharts({
  history,
  npuSupported = true,
}: TrendChartsProps) {
  const { t } = useTranslation('system')
  // 受控页签：未激活页签不挂载图表，否则 ResponsiveContainer 会量到 0 宽并在
  // 控制台刷 `width(-1) ... should be greater than 0`。
  const [activeTab, setActiveTab] = useState('compute')

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
      <Tabs value={activeTab} onValueChange={setActiveTab} className='w-full'>
        <CardHeader className='flex flex-col gap-3 p-0 sm:flex-row sm:items-center sm:justify-between'>
          <div className='flex items-center gap-2.5'>
            <div className='flex size-7 shrink-0 items-center justify-center rounded-lg border border-border/70 bg-muted/40 text-muted-foreground'>
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
                {t('charts.tabCompute', {
                  defaultValue: '计算负载 (CPU / NPU)',
                })}
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
                {t('charts.tabNetwork', { defaultValue: '网络吞吐速率' })}
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
            <div className={CHART_AREA_CLASS}>
              <div className='flex h-full items-center justify-center rounded-lg border border-dashed border-border/60 bg-muted/20 text-xs text-muted-foreground'>
                {t('charts.emptyHistory', {
                  defaultValue: '历史时序数据采集积累中...',
                })}
              </div>
            </div>
          ) : (
            <>
              {/* 1. Compute Tab (CPU & NPU) */}
              <TabsContent value='compute' className={CHART_AREA_CLASS}>
                {activeTab === 'compute' && (
                  <ResponsiveContainer
                    width='100%'
                    height='100%'
                    initialDimension={CHART_INITIAL_DIMENSION}
                  >
                    <LineChart data={chartData} margin={CHART_MARGIN}>
                      <CartesianGrid
                        strokeDasharray='3 3'
                        className='stroke-border/30'
                      />
                      <XAxis
                        dataKey='time'
                        tickLine={false}
                        axisLine={false}
                        height={22}
                        minTickGap={32}
                        tick={AXIS_TICK}
                        interval='preserveStartEnd'
                      />
                      <YAxis
                        domain={[0, 100]}
                        ticks={[0, 25, 50, 75, 100]}
                        width={PERCENT_AXIS_WIDTH}
                        tickLine={false}
                        axisLine={false}
                        tick={AXIS_TICK}
                        tickFormatter={(v) => `${v}%`}
                      />
                      <Tooltip
                        contentStyle={TOOLTIP_STYLE}
                        formatter={(value: unknown) => [
                          `${Number(value).toFixed(1)}%`,
                        ]}
                      />
                      <Legend {...LEGEND_PROPS} />
                      <Line
                        type='monotone'
                        dataKey='cpu'
                        name={t('charts.cpuLoad', {
                          defaultValue: 'CPU 全局总负载',
                        })}
                        stroke='var(--chart-1)'
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
                          stroke='var(--chart-2)'
                          strokeWidth={2}
                          dot={false}
                          isAnimationActive={false}
                        />
                      )}
                    </LineChart>
                  </ResponsiveContainer>
                )}
              </TabsContent>

              {/* 2. Memory Tab */}
              <TabsContent value='memory' className={CHART_AREA_CLASS}>
                {activeTab === 'memory' && (
                  <ResponsiveContainer
                    width='100%'
                    height='100%'
                    initialDimension={CHART_INITIAL_DIMENSION}
                  >
                    <AreaChart data={chartData} margin={CHART_MARGIN}>
                      <defs>
                        <linearGradient
                          id='memGrad'
                          x1='0'
                          y1='0'
                          x2='0'
                          y2='1'
                        >
                          <stop
                            offset='5%'
                            stopColor='var(--chart-3)'
                            stopOpacity={0.35}
                          />
                          <stop
                            offset='95%'
                            stopColor='var(--chart-3)'
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
                        height={22}
                        minTickGap={32}
                        tick={AXIS_TICK}
                        interval='preserveStartEnd'
                      />
                      <YAxis
                        domain={[0, 100]}
                        ticks={[0, 25, 50, 75, 100]}
                        width={PERCENT_AXIS_WIDTH}
                        tickLine={false}
                        axisLine={false}
                        tick={AXIS_TICK}
                        tickFormatter={(v) => `${v}%`}
                      />
                      <Tooltip
                        contentStyle={TOOLTIP_STYLE}
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
                        stroke='var(--chart-3)'
                        fillOpacity={1}
                        fill='url(#memGrad)'
                        strokeWidth={2}
                        isAnimationActive={false}
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                )}
              </TabsContent>

              {/* 3. Network Tab */}
              <TabsContent value='network' className={CHART_AREA_CLASS}>
                {activeTab === 'network' && (
                  <ResponsiveContainer
                    width='100%'
                    height='100%'
                    initialDimension={CHART_INITIAL_DIMENSION}
                  >
                    <AreaChart data={chartData} margin={CHART_MARGIN}>
                      <defs>
                        <linearGradient id='rxGrad' x1='0' y1='0' x2='0' y2='1'>
                          <stop
                            offset='5%'
                            stopColor='var(--chart-1)'
                            stopOpacity={0.35}
                          />
                          <stop
                            offset='95%'
                            stopColor='var(--chart-1)'
                            stopOpacity={0.0}
                          />
                        </linearGradient>
                        <linearGradient id='txGrad' x1='0' y1='0' x2='0' y2='1'>
                          <stop
                            offset='5%'
                            stopColor='var(--chart-2)'
                            stopOpacity={0.35}
                          />
                          <stop
                            offset='95%'
                            stopColor='var(--chart-2)'
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
                        height={22}
                        minTickGap={32}
                        tick={AXIS_TICK}
                        interval='preserveStartEnd'
                      />
                      <YAxis
                        width={RATE_AXIS_WIDTH}
                        tickLine={false}
                        axisLine={false}
                        tick={AXIS_TICK}
                        tickFormatter={(v) => formatRate(Number(v))}
                      />
                      <Tooltip
                        contentStyle={TOOLTIP_STYLE}
                        formatter={(value: unknown, name?: unknown) => [
                          formatRate(Number(value)),
                          String(name ?? ''),
                        ]}
                      />
                      <Legend {...LEGEND_PROPS} />
                      <Area
                        type='monotone'
                        dataKey='netRx'
                        name={t('charts.rxRate', {
                          defaultValue: '下行吞吐 (Rx)',
                        })}
                        stroke='var(--chart-1)'
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
                        stroke='var(--chart-2)'
                        fillOpacity={1}
                        fill='url(#txGrad)'
                        strokeWidth={1.5}
                        isAnimationActive={false}
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                )}
              </TabsContent>

              {/* 4. Thermal Tab */}
              <TabsContent value='temperature' className={CHART_AREA_CLASS}>
                {activeTab === 'temperature' && (
                  <ResponsiveContainer
                    width='100%'
                    height='100%'
                    initialDimension={CHART_INITIAL_DIMENSION}
                  >
                    <LineChart data={chartData} margin={CHART_MARGIN}>
                      <CartesianGrid
                        strokeDasharray='3 3'
                        className='stroke-border/30'
                      />
                      <XAxis
                        dataKey='time'
                        tickLine={false}
                        axisLine={false}
                        height={22}
                        minTickGap={32}
                        tick={AXIS_TICK}
                        interval='preserveStartEnd'
                      />
                      <YAxis
                        domain={[20, 100]}
                        ticks={[20, 40, 60, 80, 100]}
                        width={TEMP_AXIS_WIDTH}
                        tickLine={false}
                        axisLine={false}
                        tick={AXIS_TICK}
                        tickFormatter={(v) => `${v}°C`}
                      />
                      <Tooltip
                        contentStyle={TOOLTIP_STYLE}
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
                        stroke='var(--chart-4)'
                        strokeWidth={2}
                        dot={false}
                        isAnimationActive={false}
                      />
                    </LineChart>
                  </ResponsiveContainer>
                )}
              </TabsContent>
            </>
          )}
        </CardContent>
      </Tabs>
    </Card>
  )
}
