import { useEffect, useMemo, useState } from 'react'

interface LiveClockOptions {
  serverTimeStr?: string
  timezone?: string
}

interface LiveClockResult {
  dateString: string
  timeString: string
  fullFormatted: string
  virtualDate: Date
}

export function useLiveClock({ serverTimeStr, timezone }: LiveClockOptions): LiveClockResult {
  const [prevServerTimeStr, setPrevServerTimeStr] = useState(serverTimeStr)
  const [ticks, setTicks] = useState(0)

  // Reset ticks during render when a new server time string arrives
  if (serverTimeStr !== prevServerTimeStr) {
    setPrevServerTimeStr(serverTimeStr)
    setTicks(0)
  }

  useEffect(() => {
    const timer = setInterval(() => {
      setTicks((t) => t + 1)
    }, 1000)

    return () => clearInterval(timer)
  }, [])

  const tz = timezone || 'Asia/Shanghai'

  const formatters = useMemo(() => {
    try {
      const opts = { timeZone: tz, hour12: false }
      return {
        date: new Intl.DateTimeFormat('zh-Hans-CN', { ...opts, year: 'numeric', month: '2-digit', day: '2-digit' }),
        time: new Intl.DateTimeFormat('zh-Hans-CN', { ...opts, hour: '2-digit', minute: '2-digit', second: '2-digit' }),
        full: new Intl.DateTimeFormat('zh-Hans-CN', { ...opts, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }),
      }
    } catch {
      return null
    }
  }, [tz])

  return useMemo(() => {
    const baseServerMs = serverTimeStr ? new Date(serverTimeStr).getTime() : NaN
    const currentMs = !isNaN(baseServerMs)
      ? baseServerMs + ticks * 1000
      : Date.UTC(2026, 0, 1) + ticks * 1000

    const date = new Date(currentMs)

    if (formatters) {
      return {
        dateString: formatters.date.format(date),
        timeString: formatters.time.format(date),
        fullFormatted: formatters.full.format(date),
        virtualDate: date,
      }
    }

    return {
      dateString: date.toLocaleDateString(),
      timeString: date.toLocaleTimeString(),
      fullFormatted: date.toLocaleString(),
      virtualDate: date,
    }
  }, [serverTimeStr, ticks, formatters])
}
